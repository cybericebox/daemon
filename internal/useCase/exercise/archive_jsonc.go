package exercise

// stripJSONComments blanks out `// …` (to end of line) and `/* … */` comments outside string
// literals, in one pass. Comment bytes become spaces and newlines inside a block comment stay,
// so the length of the input and every decoder offset and line number are unchanged. Nothing
// else is touched: a trailing comma is still a decoder error.
func stripJSONComments(in []byte) ([]byte, error) {
	out := make([]byte, len(in))
	copy(out, in)
	inString := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		if inString {
			switch c {
			case '\\':
				i++ // the escaped byte (a quote included) never ends the string
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c != '/' || i+1 >= len(in) {
			continue
		}
		switch in[i+1] {
		case '/':
			for ; i < len(in) && in[i] != '\n'; i++ {
				out[i] = ' '
			}
			i-- // the newline itself is kept by the loop increment
		case '*':
			end := -1
			for j := i + 2; j+1 < len(in); j++ {
				if in[j] == '*' && in[j+1] == '/' {
					end = j + 1
					break
				}
			}
			if end < 0 {
				return nil, archiveInvalid("unterminated comment")
			}
			for ; i <= end; i++ {
				if in[i] != '\n' {
					out[i] = ' '
				}
			}
			i--
		}
	}
	return out, nil
}

// pickExerciseDocument returns the one document at the task folder root: exercise.json xor
// exercise.yaml.
func pickExerciseDocument(files map[string][]byte) (string, []byte, error) {
	jsonBody, hasJSON := files["exercise.json"]
	yamlBody, hasYAML := files["exercise.yaml"]
	switch {
	case hasJSON && hasYAML:
		return "", nil, archiveBothDocuments()
	case hasJSON:
		return "exercise.json", jsonBody, nil
	case hasYAML:
		return "exercise.yaml", yamlBody, nil
	}
	return "", nil, archiveInvalid("missing exercise.json or exercise.yaml")
}
