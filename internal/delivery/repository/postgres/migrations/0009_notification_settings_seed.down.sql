DELETE
FROM notification_settings
WHERE channel = 'email'
  AND notification_type IN (
                            'continue_registration',
                            'password_reset',
                            'email_confirmation',
                            'user_invitation',
                            'account_exists'
    );
