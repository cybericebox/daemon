-- The start configuration (entrypoint, cmd, user, working directory) of an image by reference to an external registry,
-- read from its manifest and config blob (no layer) when an exercise that starts a device through the launcher is published,
-- so a deploy never needs the registry for it. Keyed by the normalized reference and the credential scope it was read with
-- ('' = public, else the event whose registry secret was used): a private image's configuration is never given to another
-- event. digest is the manifest (or index) digest the reference had then.
CREATE TABLE external_image_configs
(
    reference   text        NOT NULL,
    scope       text        NOT NULL DEFAULT '',
    digest      text        NOT NULL,
    service     jsonb       NOT NULL,
    resolved_at timestamptz NOT NULL,
    PRIMARY KEY (reference, scope)
);
