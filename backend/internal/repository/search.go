package repository

import (
	"context"

	"ludiskus/internal/domain"
)

// SearchTopics tìm topic published trong phạm vi spaceUUIDs (Space người dùng
// được xem) bằng Postgres FTS, có rank + highlight (docs/06).
func (r *Repo) SearchTopics(ctx context.Context, q string, spaceUUIDs []string, boardID, authorUUID, topicType string, limit, offset int) ([]domain.Topic, error) {
	if len(spaceUUIDs) == 0 {
		return []domain.Topic{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+topicColsT+`,
		       ts_rank(t.search_tsv, query) AS rank,
		       ts_headline('simple', unaccent(t.title), query,
		                   'StartSel=<mark>,StopSel=</mark>') AS hl
		FROM topics t, websearch_to_tsquery('simple', unaccent($1)) query
		WHERE t.search_tsv @@ query
		  AND t.status = 'published'
		  AND t.space_uuid = ANY($2)
		  AND ($3 = '' OR t.board_id = $3::uuid)
		  AND ($4 = '' OR t.author_profile_uuid = $4::uuid)
		  AND ($5 = '' OR t.type = $5::topic_type)
		ORDER BY rank DESC, t.last_post_at DESC NULLS LAST
		LIMIT $6 OFFSET $7`,
		q, spaceUUIDs, boardID, authorUUID, topicType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Topic{}
	for rows.Next() {
		var t domain.Topic
		if err := rows.Scan(&t.ID, &t.SpaceUUID, &t.BoardID, &t.AuthorProfileUUID, &t.Title, &t.Slug,
			&t.Type, &t.Status, &t.IsPinned, &t.IsResolved, &t.AnswerPostID, &t.ReplyCount,
			&t.ViewCount, &t.ReactionCount, &t.LastPostAt, &t.LastPostProfileUUID,
			&t.CreatedAt, &t.UpdatedAt, &t.Rank, &t.Highlight); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
