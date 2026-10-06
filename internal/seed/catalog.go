package seed

import (
	"encoding/json"
	"strings"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Everything the seeder creates carries a marker, and cleanup deletes only what carries it:
// exercises are named «[seed] …» and tagged "seed", the event tag starts with "seed" and its
// name with «[seed] », users live on the seed mail domain.
const (
	NamePrefix = "[seed] "
	MarkerTag  = "seed"
	// MailDomain is a reserved .test domain: nothing sent there can ever be delivered.
	MailDomain = "seed.cybericebox.test"
	// DefaultEventTag is the subdomain of the seeded event; any tag must start with TagPrefix.
	DefaultEventTag = "seedlive"
	TagPrefix       = "seed"
)

// Public images small enough to pull fast. Every device must keep running by itself, so the
// «client» is not a bare alpine/busybox (it exits at once): the lab would report it as failed.
const (
	imageWeb    = "nginx:alpine"
	imageWeb2   = "httpd:alpine"
	imageStore  = "redis:alpine"
	imageWhoami = "traefik/whoami:v1.10.3"
)

var idNamespace = uuid.Must(uuid.FromString("5eed5eed-0000-4000-8000-000000000001"))

// seedID derives a stable id, so a re-run (or a re-publish) keeps every variant, task, hint and
// device id and a board built on them stays valid.
func seedID(parts ...string) uuid.UUID {
	return uuid.NewV5(idNamespace, strings.Join(parts, "/"))
}

type (
	exerciseSpec struct {
		Key         string
		Name        string
		Description string
		Tags        []string
		Variants    []variantSpec
		// Group is the event challenge group the exercise's tasks go to.
		Group string
		// Chain makes each task require the previous one on the event board.
		Chain bool
	}

	variantSpec struct {
		Note    string
		Devices []deviceSpec
		// Switch cables the devices (and the VPN) through one unmanaged switch.
		Switch bool
		// DHCP gives the VPN network a DHCP range; devices with Host 0 lease from it.
		DHCP  bool
		Tasks []taskSpec
	}

	deviceSpec struct {
		Key     string
		Name    string
		Image   string
		Web     bool   // exposed over http on port 80
		Persist string // "" off, "default" on with the platform debounce, else a Go duration
		Host    int32  // host number in the VPN subnet; 0 = DHCP preset
		Preset  exerciseModel.SecurityPreset
	}

	taskSpec struct {
		Key        string
		Name       string
		Difficulty exerciseModel.Difficulty
		Flag       string
		Text       []string
		Hints      []hintSpec
		// Device and FlagVar hand the flag to a lab device through an env var.
		Device  string
		FlagVar string
	}

	hintSpec struct {
		Level exerciseModel.HintLevel
		Text  string
	}
)

// lexical is the editor's rich-text document: the same shape the exercise editor saves.
func lexical(lines ...string) map[string]any {
	paragraphs := make([]any, 0, len(lines))
	for _, line := range lines {
		paragraphs = append(paragraphs, map[string]any{
			"children":  []any{map[string]any{"detail": 0, "format": 0, "mode": "normal", "style": "", "text": line, "type": "text", "version": 1}},
			"direction": nil, "format": "", "indent": 0, "type": "paragraph", "version": 1, "textFormat": 0, "textStyle": "",
		})
	}
	return map[string]any{"root": map[string]any{"children": paragraphs, "direction": nil, "format": "", "indent": 0, "type": "root", "version": 1}}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

// variants builds the stored variants of the spec: device wiring, tasks, hints.
func (e exerciseSpec) variants() []exerciseModel.Variant {
	out := make([]exerciseModel.Variant, 0, len(e.Variants))
	for index, spec := range e.Variants {
		variantKey := "v" + string(rune('0'+index))
		topology, deviceIDs := spec.topology(e.Key, variantKey)
		tasks := make([]exerciseModel.Task, 0, len(spec.Tasks))
		for _, task := range spec.Tasks {
			built := exerciseModel.Task{
				ID:          seedID(e.Key, "task", task.Key),
				Name:        task.Name,
				Description: mustJSON(lexical(task.Text...)),
				Difficulty:  task.Difficulty,
				Flag:        []string{task.Flag},
			}
			if task.Device != "" {
				built.LinkedDeviceID = uuid.NullUUID{UUID: deviceIDs[task.Device], Valid: true}
				built.DeviceFlagVar = task.FlagVar
			}
			for i, hint := range task.Hints {
				built.Hints = append(built.Hints, exerciseModel.Hint{
					ID: seedID(e.Key, "hint", task.Key, string(rune('0'+i))), Level: hint.Level, Text: string(mustJSON(lexical(hint.Text))),
				})
			}
			tasks = append(tasks, built)
		}
		out = append(out, exerciseModel.Variant{
			ID: seedID(e.Key, "variant", variantKey), Index: int32(index), Note: spec.Note, Tasks: tasks, Topology: topology,
		})
	}
	return out
}

func (v variantSpec) topology(exerciseKey, variantKey string) (exerciseModel.Topology, map[string]uuid.UUID) {
	topology := exerciseModel.Topology{Devices: []exerciseModel.Device{}, Connections: []exerciseModel.Connection{}}
	ids := make(map[string]uuid.UUID, len(v.Devices))
	if len(v.Devices) == 0 {
		return topology, ids
	}
	topology.VPN = exerciseModel.NetworkSpec{Enabled: true}
	if v.DHCP {
		topology.VPN.DHCP = true
		topology.VPN.DHCPRanges = []exerciseModel.DHCPRange{{Start: 100, End: 200}}
	}
	device := func(key string) uuid.UUID { return seedID(exerciseKey, variantKey, "device", key) }
	deviceEndpoint := func(id uuid.UUID, iface string) exerciseModel.Endpoint {
		return exerciseModel.Endpoint{Kind: exerciseModel.EndpointDevice, DeviceID: id, Interface: iface}
	}
	vpn := exerciseModel.Endpoint{Kind: exerciseModel.EndpointVPN, Interface: "eth0"}
	connect := func(a, b exerciseModel.Endpoint) {
		topology.Connections = append(topology.Connections, exerciseModel.Connection{Endpoints: []exerciseModel.Endpoint{a, b}})
	}

	var switchID uuid.UUID
	if v.Switch {
		switchID = device("sw")
		topology.Devices = append(topology.Devices, exerciseModel.Device{ID: switchID, Name: "sw", Type: exerciseModel.DeviceTypeUnmanagedSwitch})
		connect(vpn, deviceEndpoint(switchID, "GigabitEthernet0/1"))
	}
	for index, spec := range v.Devices {
		id := device(spec.Key)
		ids[spec.Key] = id
		ip := exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeDHCPPreset}
		if spec.Host > 0 {
			ip = exerciseModel.IPConfig{Type: exerciseModel.IPConfigTypeStatic, AddressRef: &exerciseModel.NetworkIPRef{Network: "vpn", Host: spec.Host}}
		}
		built := exerciseModel.Device{
			ID: id, Name: spec.Name, Type: exerciseModel.DeviceTypeContainer, Image: spec.Image, SecurityPreset: spec.Preset,
			ResourcePreset: "small",
			Interfaces:     []exerciseModel.Interface{{Name: "eth0", IP: ip}},
			EnvVars:        []exerciseModel.EnvVar{{Name: "SEED", Value: "1"}},
		}
		if spec.Web {
			built.External = &exerciseModel.ExternalAccess{Port: 80, Protocol: "http"}
		}
		switch spec.Persist {
		case "":
		case "default":
			built.Persistence = &exerciseModel.DevicePersistence{Enabled: true}
		default:
			built.Persistence = &exerciseModel.DevicePersistence{Enabled: true, Debounce: spec.Persist}
		}
		topology.Devices = append(topology.Devices, built)
		if v.Switch {
			connect(deviceEndpoint(id, "eth0"), deviceEndpoint(switchID, "GigabitEthernet0/"+itoa(index+2)))
		} else {
			connect(deviceEndpoint(id, "eth0"), vpn)
		}
	}
	return topology, ids
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func hint(level exerciseModel.HintLevel, text string) hintSpec {
	return hintSpec{Level: level, Text: text}
}

// catalog is the set of exercises the seeder keeps in the platform catalog: ten exercises, six
// of them with a lab (one to three devices, a switch, state persistence, DHCP, two variants),
// four static. Flags are fixed, so a live check can solve them: ICE{seed_<key>}.
func catalog() []exerciseSpec {
	const (
		nudge     = exerciseModel.HintLevelNudge
		direction = exerciseModel.HintLevelDirection
		steps     = exerciseModel.HintLevelSteps
		near      = exerciseModel.HintLevelNearSolution
	)
	return []exerciseSpec{
		{
			Key: "crypto-warmup", Name: NamePrefix + "Crypto Warm-up", Tags: []string{MarkerTag, "crypto"}, Group: "Warm-up",
			Description: "Two short ciphers to get started.",
			Variants: []variantSpec{{Tasks: []taskSpec{
				{Key: "caesar", Name: "Caesar shift", Difficulty: exerciseModel.DifficultyTrivial, Flag: "ICE{seed_caesar_shift}",
					Text:  []string{"The note says: «Fdhvdu lv vwloo hdvb». Decode it and submit the flag."},
					Hints: []hintSpec{hint(nudge, "The alphabet is shifted by a small constant."), hint(steps, "Shift every letter back by three.")}},
				{Key: "base64", Name: "Base64 layers", Difficulty: exerciseModel.DifficultyEasy, Flag: "ICE{seed_base64_layers}",
					Text:  []string{"The token was encoded more than once. Peel the layers."},
					Hints: []hintSpec{hint(direction, "Every layer is the same encoding.")}},
			}}},
		},
		{
			Key: "forensics-quiz", Name: NamePrefix + "Forensics Quiz", Tags: []string{MarkerTag, "forensics"}, Group: "Forensics",
			Description: "Questions about a seized disk image.",
			Variants: []variantSpec{{Tasks: []taskSpec{
				{Key: "metadata", Name: "Hidden metadata", Difficulty: exerciseModel.DifficultyEasy, Flag: "ICE{seed_hidden_metadata}",
					Text:  []string{"The photo says nothing, its metadata does."},
					Hints: []hintSpec{hint(nudge, "Look at what the camera wrote into the file.")}},
				{Key: "deleted", Name: "Deleted file", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_deleted_file}",
					Text:  []string{"A file was deleted from the image. Recover its content."},
					Hints: []hintSpec{hint(nudge, "Deleted is not erased."), hint(near, "Carve the image for the file header.")}},
			}}},
		},
		{
			Key: "osint-trivia", Name: NamePrefix + "OSINT Trivia", Tags: []string{MarkerTag, "osint"}, Group: "Warm-up",
			Description: "One question that needs a search engine.",
			Variants: []variantSpec{{Tasks: []taskSpec{
				{Key: "landmark", Name: "Find the landmark", Difficulty: exerciseModel.DifficultyElementary, Flag: "ICE{seed_find_landmark}",
					Text: []string{"Name the building on the photo and submit the flag."}},
			}}},
		},
		{
			Key: "logic-puzzle", Name: NamePrefix + "Logic Puzzle", Tags: []string{MarkerTag, "misc"}, Group: "Warm-up", Chain: true,
			Description: "Three puzzles, each one needs the previous answer.",
			Variants: []variantSpec{{Tasks: []taskSpec{
				{Key: "one", Name: "Puzzle one", Difficulty: exerciseModel.DifficultyElementary, Flag: "ICE{seed_puzzle_one}",
					Text: []string{"Start here."}},
				{Key: "two", Name: "Puzzle two", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_puzzle_two}",
					Text: []string{"The first answer is the key."}, Hints: []hintSpec{hint(direction, "XOR is involved.")}},
				{Key: "three", Name: "Puzzle three", Difficulty: exerciseModel.DifficultyHard, Flag: "ICE{seed_puzzle_three}",
					Text:  []string{"The last one."},
					Hints: []hintSpec{hint(nudge, "Parity."), hint(steps, "Write the bits in a grid."), hint(near, "Read the grid diagonally.")}},
			}}},
		},
		{
			Key: "web-basics", Name: NamePrefix + "Web Basics", Tags: []string{MarkerTag, "web"}, Group: "Web",
			Description: "A single web server with a flag in its environment.",
			Variants: []variantSpec{{
				Devices: []deviceSpec{{Key: "web", Name: "web", Image: imageWeb, Web: true, Host: 10, Preset: exerciseModel.SecurityPresetService}},
				Tasks: []taskSpec{{Key: "env", Name: "Server secrets", Difficulty: exerciseModel.DifficultyEasy, Flag: "ICE{seed_web_basics}", Device: "web", FlagVar: "FLAG",
					Text:  []string{"Open the web server over the VPN. The flag sits in its environment."},
					Hints: []hintSpec{hint(steps, "Find a way to list the server's environment.")}}},
			}},
		},
		{
			Key: "web-cache", Name: NamePrefix + "Web Plus Cache", Tags: []string{MarkerTag, "web", "net"}, Group: "Web",
			Description: "A web server and a cache behind one switch; the web box keeps its state.",
			Variants: []variantSpec{{
				Switch: true,
				Devices: []deviceSpec{
					{Key: "web", Name: "web", Image: imageWeb, Web: true, Persist: "5s", Host: 10, Preset: exerciseModel.SecurityPresetService},
					{Key: "cache", Name: "cache", Image: imageStore, Host: 11},
				},
				Tasks: []taskSpec{
					{Key: "web", Name: "Front door", Difficulty: exerciseModel.DifficultyEasy, Flag: "ICE{seed_front_door}", Device: "web", FlagVar: "FLAG_WEB",
						Text: []string{"Get into the web server."}, Hints: []hintSpec{hint(nudge, "Mind the other host on the same switch.")}},
					{Key: "cache", Name: "Cache dump", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_cache_dump}", Device: "cache", FlagVar: "FLAG_CACHE",
						Text:  []string{"The cache holds more than it should."},
						Hints: []hintSpec{hint(direction, "It speaks a plain text protocol."), hint(steps, "Connect to port 6379 and list the keys.")}},
				},
			}},
		},
		{
			Key: "net-recon", Name: NamePrefix + "Network Recon", Tags: []string{MarkerTag, "net"}, Group: "Network",
			Description: "Map the segment: two services and a client that leases its address.",
			Variants: []variantSpec{{
				Switch: true, DHCP: true,
				Devices: []deviceSpec{
					{Key: "gw", Name: "gateway", Image: imageWeb, Web: true, Host: 10, Preset: exerciseModel.SecurityPresetService},
					{Key: "echo", Name: "echo", Image: imageWhoami, Host: 11},
					{Key: "client", Name: "client", Image: imageWeb2},
				},
				Tasks: []taskSpec{
					{Key: "scan", Name: "Who is alive", Difficulty: exerciseModel.DifficultyTrivial, Flag: "ICE{seed_who_is_alive}", Device: "gw", FlagVar: "FLAG_SCAN",
						Text: []string{"Find every host on the segment."}},
					{Key: "echo", Name: "Echo chamber", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_echo_chamber}", Device: "echo", FlagVar: "FLAG_ECHO",
						Text: []string{"One service repeats everything it hears."}, Hints: []hintSpec{hint(direction, "Read the headers it prints back.")}},
				},
			}},
		},
		{
			Key: "variant-roulette", Name: NamePrefix + "Variant Roulette", Tags: []string{MarkerTag, "web", "crypto"}, Group: "Web",
			Description: "Every team gets its own variant: another server, another flag.",
			Variants: []variantSpec{
				{Note: "nginx", Devices: []deviceSpec{{Key: "web", Name: "web", Image: imageWeb, Web: true, Host: 10, Preset: exerciseModel.SecurityPresetService}},
					Tasks: []taskSpec{{Key: "roll", Name: "Roll the dice", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_variant_nginx}", Device: "web", FlagVar: "FLAG",
						Text: []string{"Your team has its own server."}, Hints: []hintSpec{hint(nudge, "Check the Server header.")}}}},
				{Note: "httpd", Devices: []deviceSpec{{Key: "web", Name: "web", Image: imageWeb2, Web: true, Host: 10, Preset: exerciseModel.SecurityPresetService}},
					Tasks: []taskSpec{{Key: "roll", Name: "Roll the dice", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_variant_httpd}", Device: "web", FlagVar: "FLAG",
						Text: []string{"Your team has its own server."}, Hints: []hintSpec{hint(nudge, "Check the Server header.")}}}},
			},
		},
		{
			Key: "persistent-box", Name: NamePrefix + "Persistent Box", Tags: []string{MarkerTag, "forensics", "persistence"}, Group: "Forensics", Chain: true,
			Description: "Two boxes that keep their state across a restart; the tasks go one after another.",
			Variants: []variantSpec{{
				Switch: true,
				Devices: []deviceSpec{
					{Key: "box", Name: "box", Image: imageWeb, Web: true, Persist: "3s", Host: 10, Preset: exerciseModel.SecurityPresetService},
					{Key: "store", Name: "store", Image: imageStore, Persist: "default", Host: 11},
				},
				Tasks: []taskSpec{
					{Key: "first", Name: "First foothold", Difficulty: exerciseModel.DifficultyTrivial, Flag: "ICE{seed_first_foothold}", Device: "box", FlagVar: "FLAG_1",
						Text: []string{"Get onto the box."}},
					{Key: "second", Name: "Leave a mark", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_leave_mark}", Device: "store", FlagVar: "FLAG_2",
						Text: []string{"Change something, restart the box, find it again."}, Hints: []hintSpec{hint(direction, "Restart the container and check the change is still there.")}},
					{Key: "third", Name: "Cold case", Difficulty: exerciseModel.DifficultyHard, Flag: "ICE{seed_cold_case}", Device: "box", FlagVar: "FLAG_3",
						Text:  []string{"Something was removed from the box before the restart."},
						Hints: []hintSpec{hint(nudge, "Logs survive."), hint(steps, "Search the writable layer."), hint(near, "Look for files changed after the first login.")}},
				},
			}},
		},
		{
			Key: "edge-services", Name: NamePrefix + "Edge Services", Tags: []string{MarkerTag, "net", "web"}, Group: "Network",
			Description: "Three public services on one switch.",
			Variants: []variantSpec{{
				Switch: true,
				Devices: []deviceSpec{
					{Key: "a", Name: "alpha", Image: imageWeb, Web: true, Host: 10, Preset: exerciseModel.SecurityPresetService},
					{Key: "b", Name: "bravo", Image: imageWeb2, Web: true, Host: 11, Preset: exerciseModel.SecurityPresetService},
					{Key: "c", Name: "charlie", Image: imageWhoami, Host: 12},
				},
				Tasks: []taskSpec{
					{Key: "alpha", Name: "Alpha", Difficulty: exerciseModel.DifficultyEasy, Flag: "ICE{seed_alpha}", Device: "a", FlagVar: "FLAG_A", Text: []string{"Service one."}},
					{Key: "bravo", Name: "Bravo", Difficulty: exerciseModel.DifficultyMedium, Flag: "ICE{seed_bravo}", Device: "b", FlagVar: "FLAG_B", Text: []string{"Service two."}},
					{Key: "charlie", Name: "Charlie", Difficulty: exerciseModel.DifficultyInsane, Flag: "ICE{seed_charlie}", Device: "c", FlagVar: "FLAG_C", Text: []string{"Service three."},
						Hints: []hintSpec{hint(near, "It reflects what you send.")}},
				},
			}},
		},
	}
}
