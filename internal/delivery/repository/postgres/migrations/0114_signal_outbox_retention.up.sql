-- Signal history (Privacy Policy, section "Retention"). Finished signals are
-- kept 365 days; the personal data in a payload is anonymized when the
-- account it names is deleted.

-- Per-user anonymization of deleted accounts.
CREATE INDEX signal_outbox_subject_user_idx ON signal_outbox ((payload ->> 'subject_user_id'));
CREATE INDEX signal_outbox_actor_user_idx ON signal_outbox ((payload ->> 'actor_user_id'));

-- Time-based purge of finished signals.
CREATE INDEX signal_outbox_finished_idx
    ON signal_outbox (created_at)
    WHERE status IN ('completed', 'failed');

-- signal_payload_anonymized replaces the references to the given accounts
-- with the nil UUID and, when the payload names one of them, drops the
-- personal fields a payload may carry. The event and team context stays, so
-- the signal still counts in event history.
CREATE FUNCTION signal_payload_anonymized(p_payload jsonb, p_user_ids text[])
    RETURNS jsonb
    LANGUAGE sql
    IMMUTABLE
AS
$$
SELECT CASE
           WHEN p_payload ->> 'subject_user_id' = ANY (p_user_ids) OR p_payload ->> 'actor_user_id' = ANY (p_user_ids)
               THEN (p_payload
                   || CASE WHEN p_payload ->> 'subject_user_id' = ANY (p_user_ids)
                               THEN jsonb_build_object('subject_user_id', '00000000-0000-0000-0000-000000000000')
                           ELSE '{}'::jsonb END
                   || CASE WHEN p_payload ->> 'actor_user_id' = ANY (p_user_ids)
                               THEN jsonb_build_object('actor_user_id', '00000000-0000-0000-0000-000000000000')
                           ELSE '{}'::jsonb END)
                   - ARRAY ['name', 'first_name', 'last_name', 'full_name', 'email', 'user_name', 'user_email',
                            'subject_name', 'subject_email', 'actor_name', 'actor_email', 'recipient_email']
           ELSE p_payload
           END
$$;
