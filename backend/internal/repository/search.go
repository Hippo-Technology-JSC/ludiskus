package repository

import (
	"context"
	"ludiskus/internal/domain"
	"ludiskus/internal/search"
)

var _ search.Engine = (*Repo)(nil)

// SearchForum returns one result per topic, with an anchor to the best matching post.
// Visibility and status apply to both topic and post before ranking or excerpts.
func (r *Repo) SearchForum(ctx context.Context, o search.Options) ([]domain.Topic, error) {
	if len(o.Spaces) == 0 {
		return []domain.Topic{}, nil
	}
	rows, err := r.pool.Query(ctx, `
 WITH query AS (SELECT websearch_to_tsquery('simple', unaccent($1)) q)
 SELECT `+topicColsT+`,
 GREATEST(ts_rank(t.search_tsv,query.q), COALESCE(hit.rank,0))::float8 rank,
 ts_headline('simple',replace(replace(replace(t.title,'&','&amp;'),'<','&lt;'),'>','&gt;'),query.q,'StartSel=<mark>,StopSel=</mark>') hl,
 hit.id::text,
 COALESCE(ts_headline('simple',replace(replace(replace(hit.body_md,'&','&amp;'),'<','&lt;'),'>','&gt;'),query.q,'StartSel=<mark>,StopSel=</mark>,MaxWords=35,MinWords=10'),'') snippet
 FROM topics t CROSS JOIN query
 LEFT JOIN LATERAL (
   SELECT p.id,p.body_md,ts_rank(p.search_tsv,query.q) rank FROM posts p
   WHERE p.topic_id=t.id AND p.status::text=CASE WHEN $8 IN ('published','locked') THEN 'published' ELSE $8 END
   AND p.search_tsv @@ query.q AND ($4='' OR p.author_profile_uuid=NULLIF($4,'')::uuid)
   ORDER BY rank DESC,p.created_at,p.id LIMIT 1
 ) hit ON true
 WHERE t.space_uuid=ANY($2::uuid[]) AND (t.status::text=$8 OR ($8='published' AND t.status='locked'))
 AND ($3='' OR t.board_id=NULLIF($3,'')::uuid)
 AND ($4='' OR t.author_profile_uuid=NULLIF($4,'')::uuid OR hit.id IS NOT NULL)
 AND ($5='' OR t.type::text=$5)
 AND ($6='' OR EXISTS(SELECT 1 FROM topic_tags tt JOIN tags tg ON tg.id=tt.tag_id WHERE tt.topic_id=t.id AND tg.slug=$6))
 AND ($9::timestamptz IS NULL OR t.created_at >= $9)
 AND ($10::timestamptz IS NULL OR t.created_at < $10)
 AND (($7<>'post' AND (t.search_tsv @@ query.q OR unaccent(t.title) ILIKE '%'||unaccent($1)||'%' OR similarity(unaccent(t.title),unaccent($1))>0.3)) OR hit.id IS NOT NULL)
 ORDER BY rank DESC,t.last_post_at DESC NULLS LAST,t.id
 LIMIT $11 OFFSET $12`, o.Query, o.Spaces, o.BoardID, o.AuthorUUID, o.TopicType, o.Tag, o.Kind, o.Status, o.From, o.Until, o.Limit, o.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Topic{}
	for rows.Next() {
		var t domain.Topic
		if err := rows.Scan(&t.ID, &t.SpaceUUID, &t.BoardID, &t.AuthorProfileUUID, &t.Title, &t.Slug, &t.Type, &t.Status, &t.IsPinned, &t.IsResolved, &t.AnswerPostID, &t.ReplyCount, &t.ViewCount, &t.LastPostAt, &t.LastPostProfileUUID, &t.CreatedAt, &t.UpdatedAt, &t.AssigneeProfileUUID, &t.Rank, &t.Highlight, &t.MatchedPostID, &t.Snippet); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
