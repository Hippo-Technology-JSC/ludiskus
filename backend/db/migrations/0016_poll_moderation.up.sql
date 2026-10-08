UPDATE comment_policies SET config=jsonb_set(config,'{poll}', '{"enabled":true,"max_per_comment":1}'::jsonb) WHERE NOT (config ? 'poll') AND ((service_code='ludiskus' AND resource_type IN ('topic','post','reply','comment')) OR (service_code='lumuse' AND resource_type IN ('movie','album','playlist')) OR (service_code='lufami' AND resource_type IN ('family_post','calendar_event')) OR (service_code='luprojet' AND resource_type='project') OR (service_code='luxtory' AND resource_type='document') OR (service_code='presentation' AND resource_type='presentation'));

CREATE INDEX idx_reports_poll ON reports (target_id, status)
  WHERE target_type IN ('poll','poll_option');
CREATE INDEX idx_moderation_poll ON moderation_items (target_id, state)
  WHERE target_type IN ('poll','poll_option');

-- QĐ-P17: khoá poll trong policy bình luận, mặc định TẮT. Không ghi đè hàng đã có khoá.
UPDATE comment_policies
   SET config = config || '{"poll":{"enabled":false,"max_per_comment":1}}'::jsonb
 WHERE NOT (config ? 'poll');

CREATE UNIQUE INDEX reports_poll_person_unique ON reports(target_type,target_id,reporter_profile_uuid) WHERE target_type IN ('poll','poll_option');
