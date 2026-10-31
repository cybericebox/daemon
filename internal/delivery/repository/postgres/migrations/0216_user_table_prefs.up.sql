-- How a person likes a data table: column order, widths, hidden columns, sort, page size. One row per person
-- and table; the JSON is validated and capped by the use case.
CREATE TABLE user_table_prefs
(
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    table_key  text        NOT NULL,
    prefs      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, table_key)
);
