package voice

import (
	"fmt"
	"regexp"
)

// Model is a pinned whisper.cpp model from the whisper.cpp model repository
// on Hugging Face. The size and SHA256 are those of the file's LFS entry.
type Model struct {
	Name    string // what the user types: base.en, base, small
	Title   string // for messages
	File    string
	Size    int64
	SHA     string
	English bool // understands English only
}

// URL is where the model is downloaded from.
func (m Model) URL() string {
	return "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + m.File
}

// DefaultModel is used when none is chosen.
const DefaultModel = "base.en"

// Models lists every model voice can use.
var Models = []Model{
	{Name: "base.en", Title: "English", File: "ggml-base.en.bin", Size: 147964211, SHA: "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002", English: true},
	{Name: "base", Title: "multilingual base", File: "ggml-base.bin", Size: 147951465, SHA: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe"},
	{Name: "small", Title: "multilingual small", File: "ggml-small.bin", Size: 487601967, SHA: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b"},
}

// FindModel returns the model called name; "" means DefaultModel.
func FindModel(name string) (Model, error) {
	if name == "" {
		name = DefaultModel
	}
	for _, m := range Models {
		if m.Name == name {
			return m, nil
		}
	}
	return Model{}, fmt.Errorf("unknown speech model %q. Choose base.en, base or small", name)
}

var langCode = regexp.MustCompile(`^[a-z]{2,3}$`)

// CheckLanguage accepts "auto" (and "", the default) or a 2-3 letter language code such as hi or es.
func CheckLanguage(lang string) error {
	if lang == "" || lang == "auto" || langCode.MatchString(lang) {
		return nil
	}
	return fmt.Errorf("%q is not a language code. Use 2 or 3 lowercase letters such as hi or es, or auto", lang)
}

// Language is what whisper is told to listen for: English models always en,
// otherwise the chosen code, auto when none.
func (m Model) Language(lang string) string {
	switch {
	case m.English:
		return "en"
	case lang == "":
		return "auto"
	}
	return lang
}
