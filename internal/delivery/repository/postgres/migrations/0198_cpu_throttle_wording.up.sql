-- «Впирається в ліміт» is slang: the CPU throttling notice says what happened in plain words. Only the platform template
-- that still holds the shipped wording changes; a text an admin edited is left alone.
UPDATE notification_in_app_templates
SET title = 'Пристрою не вистачає процесора',
    body = 'Пристрій «{{.device_name}}» лабораторії «{{.lab_name}}» не отримував достатньо процесорного часу в {{.percent}}% інтервалів за останні {{.window_minutes}} хв: його стримує виділений ліміт CPU.',
    updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'resources.cpu.throttled'
  AND title = 'Пристрій впирається в ліміт CPU'
  AND body = 'Пристрій «{{.device_name}}» лабораторії «{{.lab_name}}» був уповільнений лімітом CPU у {{.percent}}% періодів за останні {{.window_minutes}} хв.';
