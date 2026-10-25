UPDATE notification_in_app_templates
SET title = 'Пристрій впирається в ліміт CPU',
    body = 'Пристрій «{{.device_name}}» лабораторії «{{.lab_name}}» був уповільнений лімітом CPU у {{.percent}}% періодів за останні {{.window_minutes}} хв.',
    updated_at = now()
WHERE scope_event_id IS NULL
  AND notification_type = 'resources.cpu.throttled'
  AND title = 'Пристрою не вистачає процесора'
  AND body = 'Пристрій «{{.device_name}}» лабораторії «{{.lab_name}}» не отримував достатньо процесорного часу в {{.percent}}% інтервалів за останні {{.window_minutes}} хв: його стримує виділений ліміт CPU.';
