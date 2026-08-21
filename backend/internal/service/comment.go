package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ludiskus/internal/domain"
	"ludiskus/internal/markdown"
	"ludiskus/internal/repository"
)

type CommentThread struct {
	Target       *domain.CommentTarget      `json:"target"`
	Capabilities domain.CommentCapabilities `json:"capabilities"`
	Viewer       struct {
		Subscribed bool       `json:"subscribed"`
		Muted      bool       `json:"muted"`
		LastReadAt *time.Time `json:"lastReadAt,omitempty"`
	} `json:"viewer"`
	PollIntervalSeconds int `json:"pollIntervalSeconds"`
}

type CommentPage struct {
	Data       []domain.Comment `json:"data"`
	NextCursor string           `json:"nextCursor"`
}

type CreateCommentInput struct {
	BodyMD         string   `json:"bodyMd"`
	ParentID       *string  `json:"parentId"`
	AttachmentIDs  []string `json:"attachmentIds"`
	ActAsSpaceUUID *string  `json:"actAsSpaceUuid"`
	MarkdownMode   *string  `json:"markdownMode"`
}

func (s *Service) CommentThread(ctx context.Context, ref domain.ResourceRef, profileUUID string) (*CommentThread, error) {
	t, p, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return nil, err
	}
	out := &CommentThread{Target: t, Capabilities: s.commentCapabilities(ctx, t, profileUUID, p), PollIntervalSeconds: int(s.cfg.CommentPollInterval.Seconds())}
	if participant, e := s.repo.GetCommentParticipant(ctx, t.ID, profileUUID); e == nil {
		out.Viewer.Subscribed = true
		out.Viewer.Muted = participant.Muted
		out.Viewer.LastReadAt = participant.LastReadAt
	}
	return out, nil
}

func (s *Service) CommentETag(t *domain.CommentTarget) string {
	last := "none"
	if t.LastCommentID != nil {
		last = *t.LastCommentID
	}
	return fmt.Sprintf(`"%d-%d-%s"`, t.CommentCount, t.ReplyCount, last)
}

func (s *Service) CreateComment(ctx context.Context, ref domain.ResourceRef, profileUUID, idem string, in CreateCommentInput) (*domain.Comment, bool, error) {
	t, p, caps, err := s.ensureCommentable(ctx, ref, profileUUID)
	if err != nil {
		return nil, false, err
	}
	if profile, e := s.ident.Profile(ctx, profileUUID); e == nil && profile.CreatedAt != nil && time.Since(*profile.CreatedAt) < time.Duration(s.cfg.CommentNewProfileHours)*time.Hour {
		p.RateLimit.PerMinute = min(p.RateLimit.PerMinute, 2)
		p.MaxLinks = 0
		if p.ModerationMode == "none" || p.ModerationMode == "post" {
			p.ModerationMode = "first_comment"
		}
	}
	body := normalizeCommentBody(in.BodyMD)
	runes := []rune(body)
	if len(runes) < p.MinLength || len(runes) > p.MaxLength {
		return nil, false, fmt.Errorf("%w: nội dung có %d ký tự, giới hạn %d–%d", domain.ErrValidation, len(runes), p.MinLength, p.MaxLength)
	}
	if countCommentLinks(body) > p.MaxLinks {
		return nil, false, fmt.Errorf("%w: nội dung có quá %d liên kết", domain.ErrValidation, p.MaxLinks)
	}
	if len(in.AttachmentIDs) > p.Attachments.MaxPerComment || len(in.AttachmentIDs) > s.cfg.MaxAttachments {
		return nil, false, fmt.Errorf("%w: quá nhiều tệp đính kèm", domain.ErrValidation)
	}
	if len(in.AttachmentIDs) > 0 && !caps.CanAttach {
		return nil, false, domain.ErrCommentNotAllowed
	}
	mode := p.Markdown
	if in.MarkdownMode != nil {
		if !narrowerMarkdown(*in.MarkdownMode, p.Markdown) {
			return nil, false, fmt.Errorf("%w: mức markdown không hợp lệ", domain.ErrValidation)
		}
		mode = *in.MarkdownMode
	}
	comment := domain.Comment{TargetID: t.ID, ParentID: in.ParentID, AuthorKind: "profile", AuthorProfileUUID: &profileUUID,
		BodyMD: body, BodyHTML: s.md.RenderMode(mode, body), BodyHash: commentBodyHash(body), MarkdownMode: mode, Status: domain.CommentPublished}
	if idem != "" {
		comment.IdempotencyKey = &idem
	}
	if in.ActAsSpaceUUID != nil {
		if !canModerate(s.role(ctx, *in.ActAsSpaceUUID, profileUUID)) {
			return nil, false, domain.ErrForbidden
		}
		comment.AuthorKind = "space"
		comment.AuthorSpaceUUID = in.ActAsSpaceUUID
	}
	if in.ParentID != nil {
		parent, e := s.repo.GetComment(ctx, *in.ParentID)
		if e != nil {
			return nil, false, e
		}
		if parent.TargetID != t.ID || parent.Status == domain.CommentDeleted {
			return nil, false, domain.ErrValidation
		}
		if p.MaxDepth == 0 {
			return nil, false, domain.ErrCommentNotAllowed
		}
		comment.RootID = parent.RootID
		comment.Depth = parent.Depth + 1
		if comment.Depth > p.MaxDepth {
			comment.ReplyToProfileUUID = parent.AuthorProfileUUID
			anchor := parent
			for anchor.Depth >= p.MaxDepth {
				if anchor.ParentID == nil {
					break
				}
				anchor, e = s.repo.GetComment(ctx, *anchor.ParentID)
				if e != nil {
					return nil, false, e
				}
			}
			comment.ParentID = &anchor.ID
			comment.Depth = p.MaxDepth
		}
	}
	status, modSource, err := s.commentModerationStatus(ctx, t, p, profileUUID, body)
	if err != nil {
		return nil, false, err
	}
	comment.Status = status
	if modSource != "" {
		comment.ModerationSource = &modSource
	}
	if err = s.checkCommentRate(ctx, profileUUID, t.ID, comment.BodyHash, p); err != nil {
		return nil, false, err
	}
	mentions := s.resolveCommentMentions(ctx, t, p, body)
	notifications := []repository.CommentNotifyInsert(nil)
	if comment.Status == domain.CommentPublished {
		notifications = s.commentNotifyRows(ctx, t, &comment, p, mentions)
	}
	out, created, err := s.repo.InsertComment(ctx, repository.InsertCommentInput{Comment: comment, MentionProfileUUIDs: mentions,
		AttachmentIDs: in.AttachmentIDs, SpaceUUID: t.SpaceUUID, ModerationSource: modSource, Notifications: notifications})
	if err != nil {
		return nil, false, err
	}
	if created && out.Status == domain.CommentPublished {
		s.afterCommentPublished(ctx, t, out, p, mentions, true)
		s.syncInteractionResource(ctx, "comment", out.ID)
	} else if created && out.Status == domain.CommentPending {
		s.notifyCommentPending(ctx, t, out)
	}
	s.enrichComments(ctx, []*domain.Comment{out}, profileUUID, caps.CanModerate)
	s.clearCommentCaches(ctx, ref)
	return out, created, nil
}

func (s *Service) commentModerationStatus(ctx context.Context, t *domain.CommentTarget, p domain.CommentPolicy, profileUUID, body string) (string, string, error) {
	moderator := t.OwnerID != nil && *t.OwnerID == profileUUID
	if t.SpaceUUID != nil {
		moderator = moderator || canModerate(s.role(ctx, *t.SpaceUUID, profileUUID))
	}
	if moderator {
		return domain.CommentPublished, "", nil
	}
	if s.redis != nil {
		if override, err := s.redis.Get(ctx, "cmt:rl:override:"+profileUUID).Result(); err == nil && override == "pre_moderated" {
			return domain.CommentPending, "pre", nil
		}
	}
	banned := p.BannedWords
	if p.BannedWordsSource == "space" && t.SpaceUUID != nil {
		if f, err := s.repo.GetForum(ctx, *t.SpaceUUID); err == nil {
			banned = f.BannedWords
		}
	}
	hasBanned := matchesBanned(body, banned)
	switch p.ModerationMode {
	case "pre":
		if hasBanned {
			return domain.CommentPending, "banned_word", nil
		}
		return domain.CommentPending, "pre", nil
	case "first_comment":
		had, err := s.repo.ExistsPublishedCommentByAuthor(ctx, t.ID, profileUUID)
		if err != nil {
			return "", "", err
		}
		if !had {
			return domain.CommentPending, "first_comment", nil
		}
	}
	if hasBanned {
		return domain.CommentPublished, "banned_word", nil
	}
	return domain.CommentPublished, "", nil
}

func normalizeCommentBody(v string) string {
	var b strings.Builder
	for _, r := range v {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	lines := strings.Split(b.String(), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			blank++
			if blank > 2 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var commentLinkRE = regexp.MustCompile(`(?i)https?://[^\s<]+`)

func countCommentLinks(v string) int { return len(commentLinkRE.FindAllString(v, -1)) }
func narrowerMarkdown(chosen, maxMode string) bool {
	level := map[string]int{"plain": 0, "basic": 1, "rich": 2}
	a, ok := level[chosen]
	if !ok {
		return false
	}
	return a <= level[maxMode]
}

func (s *Service) resolveCommentMentions(ctx context.Context, t *domain.CommentTarget, p domain.CommentPolicy, body string) []string {
	if !p.Mentions.Enabled || p.Mentions.Scope == "none" {
		return nil
	}
	handles := markdown.Mentions(body)
	if len(handles) > p.Mentions.MaxPerComment {
		handles = handles[:p.Mentions.MaxPerComment]
	}
	out := []string{}
	seen := map[string]bool{}
	for _, h := range handles {
		profile, err := s.ident.ProfileByCode(ctx, h)
		if err != nil && len(h) >= 32 {
			profile, err = s.ident.Profile(ctx, h)
		}
		if err != nil || profile == nil || seen[profile.ProfileUUID] {
			continue
		}
		allowed := false
		scope := p.Mentions.Scope
		if scope == "space" && t.SpaceUUID != nil {
			allowed = s.ident.IsMember(ctx, *t.SpaceUUID, profile.ProfileUUID)
		} else {
			_, e := s.repo.GetCommentParticipant(ctx, t.ID, profile.ProfileUUID)
			allowed = e == nil || t.OwnerID != nil && *t.OwnerID == profile.ProfileUUID
		}
		if allowed {
			seen[profile.ProfileUUID] = true
			out = append(out, profile.ProfileUUID)
		}
	}
	return out
}

func (s *Service) ListComments(ctx context.Context, ref domain.ResourceRef, profileUUID, sort, cursor string, limit, preview int) (*CommentPage, error) {
	t, p, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return nil, err
	}
	if sort == "" {
		sort = p.SortDefault
	}
	if sort == "top" && !s.hipt.Enabled() {
		sort = "newest"
	}
	if !map[string]bool{"newest": true, "oldest": true, "top": true}[sort] {
		return nil, domain.ErrSortNotSupported
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if preview < 0 || preview > 10 {
		preview = 3
	}
	pinned, score, at, id, err := decodeRootCommentCursor(cursor)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListRootComments(ctx, t.ID, profileUUID, sort, pinned, score, at, id, limit+1)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeRootCommentCursor(last)
	}
	roots := make([]string, len(items))
	for i := range items {
		roots[i] = items[i].ID
	}
	replies, err := s.repo.PreviewCommentReplies(ctx, roots, profileUUID, preview)
	if err != nil {
		return nil, err
	}
	ptrs := []*domain.Comment{}
	for i := range items {
		items[i].PreviewReplies = replies[items[i].ID]
		ptrs = append(ptrs, &items[i])
		for j := range items[i].PreviewReplies {
			ptrs = append(ptrs, &items[i].PreviewReplies[j])
		}
	}
	caps := s.commentCapabilities(ctx, t, profileUUID, p)
	s.enrichComments(ctx, ptrs, profileUUID, caps.CanModerate)
	return &CommentPage{Data: items, NextCursor: next}, nil
}

func (s *Service) CommentReplies(ctx context.Context, id, profileUUID, cursor string, limit int) (*CommentPage, error) {
	root, err := s.repo.GetComment(ctx, id)
	if err != nil {
		return nil, err
	}
	t, err := s.repo.GetCommentTargetByID(ctx, root.TargetID)
	if err != nil {
		return nil, err
	}
	_, p, err := s.ensureCommentReadable(ctx, t.Ref(), profileUUID)
	if err != nil {
		return nil, err
	}
	at, cid, err := decodeCommentCursor(cursor)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	items, err := s.repo.ListCommentReplies(ctx, root.RootID, profileUUID, at, cid, limit+1)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCommentCursor(last.CreatedAt, last.ID)
	}
	ptrs := make([]*domain.Comment, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	s.enrichComments(ctx, ptrs, profileUUID, s.commentCapabilities(ctx, t, profileUUID, p).CanModerate)
	return &CommentPage{Data: items, NextCursor: next}, nil
}

func encodeCommentCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func encodeRootCommentCursor(c domain.Comment) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("v2|%t|%d|%s|%s", c.IsPinned, c.ScoreCache, c.CreatedAt.UTC().Format(time.RFC3339Nano), c.ID)))
}

func decodeRootCommentCursor(v string) (*bool, *int64, *time.Time, string, error) {
	if v == "" {
		return nil, nil, nil, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, nil, nil, "", domain.ErrInvalidCursor
	}
	parts := strings.SplitN(string(raw), "|", 5)
	if len(parts) != 5 || parts[0] != "v2" {
		return nil, nil, nil, "", domain.ErrInvalidCursor
	}
	pinned, err := strconv.ParseBool(parts[1])
	if err != nil {
		return nil, nil, nil, "", domain.ErrInvalidCursor
	}
	score, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return nil, nil, nil, "", domain.ErrInvalidCursor
	}
	at, err := time.Parse(time.RFC3339Nano, parts[3])
	if err != nil || at.After(time.Now().Add(time.Hour)) {
		return nil, nil, nil, "", domain.ErrInvalidCursor
	}
	return &pinned, &score, &at, parts[4], nil
}
func decodeCommentCursor(v string) (*time.Time, string, error) {
	if v == "" {
		return nil, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, "", domain.ErrInvalidCursor
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return nil, "", domain.ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || t.After(time.Now().Add(time.Hour)) {
		return nil, "", domain.ErrInvalidCursor
	}
	return &t, parts[1], nil
}

func (s *Service) enrichComments(ctx context.Context, items []*domain.Comment, viewer string, moderator bool) {
	uuids := []string{}
	ids := []string{}
	for _, c := range items {
		ids = append(ids, c.ID)
		if c.AuthorProfileUUID != nil {
			uuids = append(uuids, *c.AuthorProfileUUID)
		}
		if c.ReplyToProfileUUID != nil {
			uuids = append(uuids, *c.ReplyToProfileUUID)
		}
	}
	profiles := s.ident.ProfileMap(ctx, uuids)
	attachments, _ := s.repo.AttachmentsForComments(ctx, ids)
	for _, c := range items {
		if c.Status == domain.CommentDeleted {
			c.Deleted = true
			c.DeletedByAuthor = c.AuthorProfileUUID != nil && c.DeletedBy != nil && *c.AuthorProfileUUID == *c.DeletedBy
			c.BodyMD = ""
			c.BodyHTML = ""
			c.AuthorProfileUUID = nil
			c.Author = nil
		} else if c.AuthorProfileUUID != nil {
			c.Author = profiles[*c.AuthorProfileUUID]
		}
		if c.ReplyToProfileUUID != nil {
			c.ReplyToProfile = profiles[*c.ReplyToProfileUUID]
		}
		c.Attachments = attachments[c.ID]
		c.Mentions, _ = s.repo.CommentMentions(ctx, c.ID)
		c.CanEdit = c.AuthorProfileUUID != nil && *c.AuthorProfileUUID == viewer
		c.CanDelete = c.CanEdit || moderator
		c.CanModerate = moderator
	}
}

func (s *Service) GetComment(ctx context.Context, id, profileUUID string) (*domain.Comment, *domain.CommentTarget, error) {
	c, err := s.repo.GetComment(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	t, err := s.repo.GetCommentTargetByID(ctx, c.TargetID)
	if err != nil {
		return nil, nil, err
	}
	_, p, err := s.ensureCommentReadable(ctx, t.Ref(), profileUUID)
	if err != nil {
		return nil, nil, err
	}
	moderator := s.commentCapabilities(ctx, t, profileUUID, p).CanModerate
	if c.Status != domain.CommentPublished && c.Status != domain.CommentDeleted && (c.AuthorProfileUUID == nil || *c.AuthorProfileUUID != profileUUID) && !moderator {
		return nil, nil, domain.ErrNotFound
	}
	s.enrichComments(ctx, []*domain.Comment{c}, profileUUID, moderator)
	return c, t, nil
}

func (s *Service) UpdateComment(ctx context.Context, id, profileUUID string, in CreateCommentInput) (*domain.Comment, error) {
	c, t, err := s.GetComment(ctx, id, profileUUID)
	if err != nil {
		return nil, err
	}
	if c.AuthorProfileUUID == nil || *c.AuthorProfileUUID != profileUUID {
		return nil, domain.ErrForbidden
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return nil, err
	}
	if c.Status == domain.CommentPublished && time.Since(c.CreatedAt) > time.Duration(p.EditWindowMinutes)*time.Minute {
		return nil, domain.ErrEditWindowClosed
	}
	body := normalizeCommentBody(in.BodyMD)
	if n := len([]rune(body)); n < p.MinLength || n > p.MaxLength {
		return nil, domain.ErrValidation
	}
	mode := p.Markdown
	if in.MarkdownMode != nil && narrowerMarkdown(*in.MarkdownMode, p.Markdown) {
		mode = *in.MarkdownMode
	}
	out, err := s.repo.UpdateCommentBody(ctx, c, body, s.md.RenderMode(mode, body), commentBodyHash(body), mode, profileUUID)
	if err == nil {
		s.enrichComments(ctx, []*domain.Comment{out}, profileUUID, false)
		s.clearCommentCaches(ctx, t.Ref())
	}
	return out, err
}

func (s *Service) DeleteComment(ctx context.Context, id, profileUUID string) (*domain.Comment, error) {
	c, t, err := s.GetComment(ctx, id, profileUUID)
	if err != nil {
		return nil, err
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return nil, err
	}
	moderator := s.commentCapabilities(ctx, t, profileUUID, p).CanModerate
	if (c.AuthorProfileUUID == nil || *c.AuthorProfileUUID != profileUUID) && !moderator {
		return nil, domain.ErrForbidden
	}
	if !p.DeleteOwn && !moderator {
		return nil, domain.ErrForbidden
	}
	out, err := s.repo.TransitionComment(ctx, id, domain.CommentDeleted, profileUUID, "deleted")
	if err == nil {
		s.invalidateInteractionResource(ctx, "comment", id, "deleted")
		s.clearCommentCaches(ctx, t.Ref())
		s.enrichComments(ctx, []*domain.Comment{out}, profileUUID, moderator)
	}
	return out, err
}

func (s *Service) PinComment(ctx context.Context, id, profileUUID string, pinned bool) (*domain.Comment, error) {
	c, t, err := s.GetComment(ctx, id, profileUUID)
	if err != nil {
		return nil, err
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return nil, err
	}
	owner := t.OwnerID != nil && *t.OwnerID == profileUUID
	moderator := t.SpaceUUID != nil && canModerate(s.role(ctx, *t.SpaceUUID, profileUUID))
	allowed := p.Pin.Enabled && ((owner && (p.Pin.By == "owner" || p.Pin.By == "both")) || (moderator && (p.Pin.By == "moderator" || p.Pin.By == "both")))
	if !allowed {
		return nil, domain.ErrForbidden
	}
	out, err := s.repo.SetCommentPinned(ctx, c.ID, pinned, profileUUID, p.Pin.MaxPinned)
	if err == nil {
		s.clearCommentCaches(ctx, t.Ref())
	}
	return out, err
}

func (s *Service) ModerateComment(ctx context.Context, id, profileUUID, action, reason string) (*domain.Comment, error) {
	c, t, err := s.GetComment(ctx, id, profileUUID)
	if err != nil {
		return nil, err
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return nil, err
	}
	if !s.commentCapabilities(ctx, t, profileUUID, p).CanModerate {
		return nil, domain.ErrForbidden
	}
	status := map[string]string{"hide": domain.CommentHidden, "restore": domain.CommentPublished, "delete": domain.CommentDeleted}[action]
	if status == "" {
		return nil, domain.ErrValidation
	}
	out, err := s.repo.TransitionComment(ctx, c.ID, status, profileUUID, reason)
	if err == nil {
		if status == domain.CommentPublished {
			s.syncInteractionResource(ctx, "comment", id)
		} else {
			s.invalidateInteractionResource(ctx, "comment", id, "visibility")
		}
		s.clearCommentCaches(ctx, t.Ref())
	}
	return out, err
}

func (s *Service) SearchComments(ctx context.Context, ref domain.ResourceRef, profileUUID, q string) ([]domain.Comment, error) {
	t, p, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return nil, err
	}
	if len([]rune(strings.TrimSpace(q))) < 2 {
		return nil, domain.ErrValidation
	}
	items, err := s.repo.SearchComments(ctx, t.ID, q, 50)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*domain.Comment, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	s.enrichComments(ctx, ptrs, profileUUID, s.commentCapabilities(ctx, t, profileUUID, p).CanModerate)
	return items, nil
}

type CommentSummary struct {
	Ref           domain.ResourceRef `json:"ref"`
	CommentCount  int                `json:"commentCount"`
	ReplyCount    int                `json:"replyCount"`
	LastCommentAt *time.Time         `json:"lastCommentAt,omitempty"`
}

func (s *Service) CommentSummaries(ctx context.Context, refs []domain.ResourceRef, profileUUID string) ([]CommentSummary, []map[string]any, error) {
	if len(refs) > s.cfg.CommentBatchMax {
		return nil, nil, domain.ErrValidation
	}
	data := []CommentSummary{}
	skipped := []map[string]any{}
	for _, ref := range refs {
		t, _, err := s.ensureCommentReadable(ctx, ref, profileUUID)
		if err != nil {
			skipped = append(skipped, map[string]any{"ref": ref, "reason": errorCode(err)})
			continue
		}
		data = append(data, CommentSummary{Ref: ref, CommentCount: t.CommentCount, ReplyCount: t.ReplyCount, LastCommentAt: t.LastCommentAt})
	}
	return data, skipped, nil
}

func (s *Service) CommentMine(ctx context.Context, profileUUID, status, serviceCode, q string) ([]domain.Comment, error) {
	if profileUUID == "" {
		return nil, domain.ErrUnauthorized
	}
	items, err := s.repo.MineComments(ctx, profileUUID, status, serviceCode, q, 100)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*domain.Comment, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	s.enrichComments(ctx, ptrs, profileUUID, false)
	return items, nil
}

func (s *Service) SetCommentSubscription(ctx context.Context, ref domain.ResourceRef, profileUUID string, muted bool) error {
	t, _, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return err
	}
	_, err = s.repo.UpsertCommentParticipant(ctx, domain.CommentParticipant{TargetID: t.ID, ProfileUUID: profileUUID, Reason: "manual", Muted: muted})
	return err
}
func (s *Service) RemoveCommentSubscription(ctx context.Context, ref domain.ResourceRef, profileUUID string) error {
	t, _, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return err
	}
	return s.repo.RemoveManualCommentParticipant(ctx, t.ID, profileUUID)
}
func (s *Service) MarkCommentRead(ctx context.Context, ref domain.ResourceRef, profileUUID string) error {
	t, _, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return err
	}
	return s.repo.MarkCommentRead(ctx, t.ID, profileUUID)
}

func (s *Service) CommentRevisions(ctx context.Context, id, profileUUID string) ([]domain.CommentRevision, error) {
	c, _, err := s.GetComment(ctx, id, profileUUID)
	if err != nil {
		return nil, err
	}
	if c.AuthorProfileUUID == nil || *c.AuthorProfileUUID != profileUUID && !c.CanModerate {
		return nil, domain.ErrForbidden
	}
	return s.repo.CommentRevisions(ctx, id, s.cfg.CommentMaxRevisions)
}

func (s *Service) MentionSuggestions(ctx context.Context, ref domain.ResourceRef, profileUUID, q string) ([]domain.CachedProfile, error) {
	t, p, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return nil, err
	}
	if !p.Mentions.Enabled {
		return []domain.CachedProfile{}, nil
	}
	out := []domain.CachedProfile{}
	seen := map[string]bool{}
	if t.SpaceUUID != nil && p.Mentions.Scope == "space" {
		members, e := s.ident.Members(ctx, *t.SpaceUUID)
		if e != nil {
			return nil, e
		}
		for _, m := range members {
			profile, e := s.ident.Profile(ctx, m.ProfileUUID)
			if e == nil && profile != nil && matchProfile(*profile, q) && !seen[profile.ProfileUUID] {
				seen[profile.ProfileUUID] = true
				out = append(out, *profile)
				if len(out) == 10 {
					break
				}
			}
		}
	} else {
		participants, e := s.repo.ListCommentParticipants(ctx, t.ID)
		if e != nil {
			return nil, e
		}
		for _, m := range participants {
			profile, e := s.ident.Profile(ctx, m.ProfileUUID)
			if e == nil && profile != nil && matchProfile(*profile, q) && !seen[profile.ProfileUUID] {
				seen[profile.ProfileUUID] = true
				out = append(out, *profile)
				if len(out) == 10 {
					break
				}
			}
		}
	}
	return out, nil
}
func matchProfile(p domain.CachedProfile, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	return q == "" || strings.Contains(strings.ToLower(p.Name), q) || p.Code != nil && strings.Contains(strings.ToLower(*p.Code), q)
}

func errorCode(err error) string {
	for code, target := range map[string]error{"INVALID_REF": domain.ErrInvalidRef, "SERVICE_NOT_REGISTERED": domain.ErrServiceNotRegistered, "RESOURCE_GONE": domain.ErrResourceGone, "COMMENT_DISABLED": domain.ErrCommentDisabled} {
		if errors.Is(err, target) {
			return code
		}
	}
	return "not_found"
}
