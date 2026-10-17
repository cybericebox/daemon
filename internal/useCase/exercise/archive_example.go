package exercise

import "embed"

// ExampleArchiveFilename is the name the example archive is downloaded under.
const ExampleArchiveFilename = "example-task.cybericebox.zip"

// exampleFS holds the example task: a commented exercise.yaml (the user-facing description of the format) and
// the two files it links.
//
//go:embed example/exercise.yaml example/files/notes.txt example/files/notes-2.txt
var exampleFS embed.FS

// exampleEntries are the example's paths, relative to the zip root and to the example folder alike.
var exampleEntries = []string{"exercise.yaml", "files/notes.txt", "files/notes-2.txt"}

// ExampleArchive builds the downloadable example task: the three files at the zip root and no manifest.json,
// as an author writes an archive by hand. Imported as it is, it creates a working published task.
func ExampleArchive() ([]byte, error) {
	files := make(map[string][]byte, len(exampleEntries))
	for _, name := range exampleEntries {
		body, err := exampleFS.ReadFile("example/" + name)
		if err != nil {
			return nil, err
		}
		files[name] = body
	}
	return BuildArchiveNoManifest(files)
}
