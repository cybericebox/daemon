package inappdefaults

// The registry credential check: a platform token or an organizer secret of the image registry stopped working,
// or works again. One pair of types per credential class (a template cannot branch). Created at daemon start
// (see startupdefaults.Seed), like the resource calendar flow. The event labels are empty for the platform tokens.
func init() {
	for _, typ := range []string{
		"registry.push_token.broken", "registry.push_token.restored",
		"registry.pull_token.broken", "registry.pull_token.restored",
		"registry.event_secret.broken", "registry.event_secret.restored",
	} {
		StartupSeeded[typ] = true
	}
	uk = append(uk,
		Template{"registry.push_token.broken", "Токен запису до сховища не працює", "Сховище образів платформи ({{.label}}) відхилило токен запису: його відкликано, він прострочений або неправильний. Оновіть токен у налаштуваннях «Панелі платформи».", ""},
		Template{"registry.push_token.restored", "Токен запису знову працює", "Сховище образів платформи ({{.label}}) знову приймає токен запису.", ""},
		Template{"registry.pull_token.broken", "Токен читання сховища не працює", "Сховище образів платформи ({{.label}}) відхилило токен читання: його відкликано, він прострочений або неправильний. Оновіть токен у налаштуваннях «Панелі платформи».", ""},
		Template{"registry.pull_token.restored", "Токен читання знову працює", "Сховище образів платформи ({{.label}}) знову приймає токен читання.", ""},
		Template{"registry.event_secret.broken", "Облікові дані сховища не працюють", "Захід «{{.event_name}}»: сховище образів {{.label}} відхилило облікові дані — токен відкликано, прострочений або неправильний. Оновіть його в «Панелі заходу».", ""},
		Template{"registry.event_secret.restored", "Облікові дані знову працюють", "Захід «{{.event_name}}»: сховище образів {{.label}} знову приймає облікові дані.", ""},
	)
	en = append(en,
		Template{"registry.push_token.broken", "Registry push token is not working", "The platform image registry ({{.label}}) refused the push token: it was revoked, expired or is wrong. Update the token in the platform panel settings.", ""},
		Template{"registry.push_token.restored", "Registry push token works again", "The platform image registry ({{.label}}) accepts the push token again.", ""},
		Template{"registry.pull_token.broken", "Registry pull token is not working", "The platform image registry ({{.label}}) refused the pull token: it was revoked, expired or is wrong. Update the token in the platform panel settings.", ""},
		Template{"registry.pull_token.restored", "Registry pull token works again", "The platform image registry ({{.label}}) accepts the pull token again.", ""},
		Template{"registry.event_secret.broken", "Registry credentials are not working", "Event «{{.event_name}}»: the image registry {{.label}} refused the credentials — the token was revoked, expired or is wrong. Update it in the event panel.", ""},
		Template{"registry.event_secret.restored", "Registry credentials work again", "Event «{{.event_name}}»: the image registry {{.label}} accepts the credentials again.", ""},
	)
}
