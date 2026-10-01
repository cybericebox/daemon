-- The product name is written «Cyber ICE Box». Platform email templates
-- (every version), in-app templates and block presets were seeded with
-- «CyberICEBox»; event-scoped templates belong to organizers and stay as is.
UPDATE notification_email_templates
SET subject   = replace(subject, 'CyberICEBox', 'Cyber ICE Box'),
    preheader = replace(preheader, 'CyberICEBox', 'Cyber ICE Box'),
    body      = replace(body::text, 'CyberICEBox', 'Cyber ICE Box')::jsonb
WHERE scope_event_id IS NULL
  AND (subject LIKE '%CyberICEBox%' OR preheader LIKE '%CyberICEBox%' OR body::text LIKE '%CyberICEBox%');

UPDATE notification_in_app_templates
SET title = replace(title, 'CyberICEBox', 'Cyber ICE Box'),
    body  = replace(body, 'CyberICEBox', 'Cyber ICE Box')
WHERE scope_event_id IS NULL
  AND (title LIKE '%CyberICEBox%' OR body LIKE '%CyberICEBox%');

UPDATE notification_email_block_presets
SET blocks = replace(blocks::text, 'CyberICEBox', 'Cyber ICE Box')::jsonb
WHERE blocks::text LIKE '%CyberICEBox%';
