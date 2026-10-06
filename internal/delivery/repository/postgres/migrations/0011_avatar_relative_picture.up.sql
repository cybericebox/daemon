-- Convert previously-stored absolute avatar URLs to relative paths so they load
-- same-origin on every subdomain (the avatar GET route is public on all edges).
UPDATE users
SET picture = '/api/auth/avatar/' || split_part(picture, '/api/auth/avatar/', 2)
WHERE picture LIKE 'http%/api/auth/avatar/%';
