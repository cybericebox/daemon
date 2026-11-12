-- One identity per provider for an account. Without it two concurrent links (or a check followed by an insert) could
-- give an account a second Google identity. Existing duplicates, if any, make the migration fail loudly: they are
-- login methods and are not deleted on their own.
CREATE UNIQUE INDEX user_provider_user_kind_uidx ON user_providers (user_id, provider);
