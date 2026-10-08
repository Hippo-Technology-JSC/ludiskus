package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"ludiskus/internal/domain"
)

func (s *Service) InteractionContext(
	ctx context.Context, resourceType, resourceID string,
) (*domain.InteractionContext, error) {
	if resourceType == "poll" {
		return s.pollInteractionContext(ctx, resourceID)
	}
	if resourceType == "comment" {
		comment, err := s.repo.GetComment(ctx, resourceID)
		if err != nil {
			return nil, err
		}
		target, err := s.repo.GetCommentTargetByID(ctx, comment.TargetID)
		if err != nil {
			return nil, err
		}
		return s.buildCommentContext(ctx, comment, target), nil
	}
	var topic *domain.Topic
	var post *domain.Post
	var err error
	switch resourceType {
	case "topic":
		topic, err = s.repo.GetTopic(ctx, resourceID)
	case "post", "reply":
		post, err = s.repo.GetPost(ctx, resourceID)
		if err == nil {
			if (resourceType == "post") != post.IsFirst {
				return nil, domain.ErrNotFound
			}
			topic, err = s.repo.GetTopic(ctx, post.TopicID)
		}
	default:
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	forum, err := s.repo.GetForum(ctx, topic.SpaceUUID)
	if err != nil {
		return nil, err
	}

	return buildTopicContext(resourceType, resourceID, topic, post, forum), nil
}
func buildTopicContext(resourceType, resourceID string, topic *domain.Topic, post *domain.Post, forum *domain.SpaceForum) *domain.InteractionContext {
	state := interactionState(topic.Status)
	if post != nil {
		postState := interactionState(post.Status)
		if postState != "active" {
			state = postState
		}
	}
	if !forum.Enabled {
		state = "gone"
	}
	visibility := "space"
	if forum.IsPublic {
		visibility = "public"
	}
	ownerID := topic.AuthorProfileUUID
	title := topic.Title
	summary := ""
	path := fmt.Sprintf("/ludiskus/s/%s/t/%s", topic.SpaceUUID, topic.ID)
	if post != nil {
		ownerID = post.AuthorProfileUUID
		summary = excerptOf(post.BodyMD)
		path += "#post-" + post.ID
	}
	spaceUUID := topic.SpaceUUID
	return &domain.InteractionContext{
		Type: resourceType, ID: resourceID, Exists: true,
		Owner:     &domain.InteractionOwner{Type: "profile", ID: ownerID},
		SpaceUUID: &spaceUUID, Visibility: visibility, State: state,
		Title: title, Summary: summary, CanonicalPath: path,
		Capabilities: json.RawMessage(`{}`),
	}
}

func interactionState(status string) string {
	switch status {
	case domain.StatusPublished, domain.StatusLocked:
		return "active"
	case domain.StatusDeleted:
		return "gone"
	default:
		return "blocked"
	}
}

func interactionResourcePayload(value *domain.InteractionContext) map[string]any {
	return map[string]any{
		"ref": map[string]string{
			"service": "ludiskus", "type": value.Type, "id": value.ID,
		},
		"spaceUuid": value.SpaceUUID, "ownerType": value.Owner.Type,
		"ownerId": value.Owner.ID, "title": value.Title, "summary": value.Summary,
		"thumbnailUrl": value.ThumbnailURL, "canonicalPath": value.CanonicalPath,
		"visibility": value.Visibility, "state": value.State,
		"capabilities": value.Capabilities,
	}
}

func (s *Service) syncInteractionResource(ctx context.Context, resourceType, resourceID string) {
	if !s.hipt.Enabled() {
		return
	}
	value, err := s.InteractionContext(ctx, resourceType, resourceID)
	if err != nil {
		return
	}
	go func() {
		_ = s.hipt.UpsertInteractionResources(
			context.WithoutCancel(ctx), []any{interactionResourcePayload(value)},
		)
	}()
}

func (s *Service) invalidateInteractionResource(
	ctx context.Context, resourceType, resourceID, reason string,
) {
	if !s.hipt.Enabled() {
		return
	}
	go func() {
		_ = s.hipt.InvalidateInteractionResources(
			context.WithoutCancel(ctx),
			[]map[string]string{{"service": "ludiskus", "type": resourceType, "id": resourceID}},
			reason,
		)
	}()
}

func (s *Service) invalidateInteractionRefs(
	ctx context.Context, refs []domain.InteractionRef, reason string,
) {
	if !s.hipt.Enabled() || len(refs) == 0 {
		return
	}
	go func() {
		callCtx := context.WithoutCancel(ctx)
		for start := 0; start < len(refs); start += 100 {
			end := min(start+100, len(refs))
			if err := s.hipt.InvalidateInteractionResources(
				callCtx, refs[start:end], reason,
			); err != nil {
				return
			}
		}
	}()
}

// ProcessInteractionBackfill chuyển snapshot lịch sử rồi xoá từng hàng staging.
// Retry an toàn nhờ unique index ở Lufami và payload S2S idempotent.
func (s *Service) ProcessInteractionBackfill(ctx context.Context, log *slog.Logger) {
	items, err := s.repo.ClaimInteractionBackfill(ctx, 100)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("claim interaction backfill", "err", err)
		}
		return
	}
	if len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	type group struct {
		resource     map[string]any
		interactions []map[string]any
	}
	groups := map[string]*group{}
	order := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
		key := item.ResourceType + ":" + item.PostID
		value := groups[key]
		if value == nil {
			meta, metaErr := s.InteractionContext(ctx, item.ResourceType, item.PostID)
			if metaErr != nil {
				_ = s.repo.FailInteractionBackfill(ctx, ids, metaErr.Error())
				return
			}
			value = &group{resource: interactionResourcePayload(meta)}
			groups[key] = value
			order = append(order, key)
		}
		value.interactions = append(value.interactions, map[string]any{
			"actorProfileUuid": item.ActorProfileUUID,
			"kind":             item.InteractionKind, "reactionCode": item.ReactionCode,
			"createdAt": item.OccurredAt,
		})
	}
	batches := make([]map[string]any, 0, len(order))
	for _, key := range order {
		value := groups[key]
		batches = append(batches, map[string]any{
			"resource": value.resource, "interactions": value.interactions,
		})
	}
	if err := s.hipt.BackfillInteractions(ctx, batches); err != nil {
		_ = s.repo.FailInteractionBackfill(ctx, ids, err.Error())
		log.Warn("interaction backfill retry", "count", len(ids), "err", err)
		return
	}
	if err := s.repo.CompleteInteractionBackfill(ctx, ids); err != nil {
		log.Error("complete interaction backfill", "err", err)
		return
	}
	log.Info("backfilled legacy interactions", "count", len(ids),
		"resources", len(batches))
}

func interactionResourceType(post *domain.Post) string {
	if post.IsFirst {
		return "post"
	}
	return "reply"
}

func (s *Service) buildCommentContext(ctx context.Context, c *domain.Comment, t *domain.CommentTarget) *domain.InteractionContext {
	owner := &domain.InteractionOwner{Type: "profile"}
	if c.AuthorProfileUUID != nil {
		owner.ID = *c.AuthorProfileUUID
	}
	if c.AuthorKind == "space" && c.AuthorSpaceUUID != nil {
		owner.Type = "space"
		owner.ID = *c.AuthorSpaceUUID
	}
	allowed, maxPer := false, 1
	if p, e := s.commentPolicy(ctx, t); e == nil {
		allowed = p.Poll.Enabled
		maxPer = p.Poll.MaxPerComment
	}
	if t.ServiceCode == "ludiskus" && t.ResourceType == "poll" {
		allowed = false
	}
	caps, _ := json.Marshal(map[string]any{"pollAllowed": allowed, "pollMaxPerComment": maxPer, "poll": allowed})
	state := interactionState(c.Status)
	if t.State == "gone" || t.State == "blocked" {
		state = t.State
	}
	return &domain.InteractionContext{Type: "comment", ID: c.ID, Exists: true, Owner: owner, SpaceUUID: t.SpaceUUID, Visibility: t.Visibility, State: state, Title: t.Title, Summary: excerptOf(c.BodyMD), ThumbnailURL: t.ThumbnailURL, CanonicalPath: commentURL(t, c.ID), Capabilities: caps}
}
func (s *Service) pollInteractionContext(ctx context.Context, id string) (*domain.InteractionContext, error) {
	p, e := s.repo.GetPoll(ctx, id)
	if e != nil {
		return nil, e
	}
	state := "blocked"
	if p.Status == "published" || p.Status == "closed" {
		state = "active"
	}
	if p.Status == "deleted" {
		state = "gone"
	}
	visibility := "private"
	if p.Visibility != nil {
		visibility = *p.Visibility
	}
	if visibility == "unlisted" {
		visibility = "authenticated"
	}
	var space *string
	if p.SpaceUUID != nil {
		space = p.SpaceUUID
	}
	if p.Target != nil {
		target, e := s.ensureCommentTarget(ctx, p.Target.Ref(), "")
		if e != nil {
			return nil, e
		}
		p.Target = target
		visibility = target.Visibility
		space = target.SpaceUUID
		if target.State != "active" {
			state = target.State
			if state == "unverified" {
				state = "blocked"
			}
		}
	}
	owner := &domain.InteractionOwner{Type: "profile"}
	if p.CreatedBy != nil {
		owner.ID = *p.CreatedBy
	}
	if p.AuthorKind == "space" && p.AuthorSpaceUUID != nil {
		owner.Type = "space"
		owner.ID = *p.AuthorSpaceUUID
	}
	if !s.cfg.PollEnabled {
		state = "blocked"
	}
	return &domain.InteractionContext{Type: "poll", ID: id, Exists: true, Owner: owner, SpaceUUID: space, Visibility: visibility, State: state, Title: p.Question, Summary: p.DescriptionMD, CanonicalPath: p.Path(), Capabilities: json.RawMessage(`{"poll":false}`)}, nil
}
