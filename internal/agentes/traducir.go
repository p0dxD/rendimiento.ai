package agentes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/ia"
	"golang.org/x/sync/singleflight"
)

// TranslateSource is how translations sign their calls to the model.
const TranslateSource = "agent: translator"

// langName is how a language is named in the translator's instructions.
var langName = map[string]string{"es": "Mexican Spanish (usted)", "en": "English"}

// Translator turns a diagnosis into the reader's language, so it is
// diagnosed once and read in either. Two readers asking for the same
// translation at once share one call.
type Translator struct {
	Model interface {
		Complete(ctx context.Context, orgID int64, source string, req ia.Request) (ia.Response, error)
	}
	group singleflight.Group
}

// Translate is text, written in from, in the language to (es or en),
// keeping its Markdown, code, log lines, names and numbers as they are.
// key names what is translated, so readers asking for it at once share
// one call.
func (t *Translator) Translate(ctx context.Context, orgID int64, key, text, from, to string) (string, error) {
	if langName[to] == "" || langName[from] == "" {
		return "", fmt.Errorf("no translation from %q to %q", from, to)
	}
	v, err, _ := t.group.Do(key+"→"+to, func() (any, error) {
		res, err := t.Model.Complete(ctx, orgID, TranslateSource, ia.Request{
			System: fmt.Sprintf("Translate the user's text from %s to %s. It is a short Markdown explanation of why a CI run failed. "+
				"Keep the Markdown as it is (bold, lists, code spans and code blocks). Never translate what is inside code spans or code blocks, "+
				"log lines, file names, commands, names or numbers. Answer with the translation only, nothing before or after it.",
				langName[from], langName[to]),
			Messages:  []ia.Message{{Role: "user", Content: text}},
			MaxTokens: 2000,
		})
		if err != nil {
			return "", err
		}
		out := strings.TrimSpace(res.Text)
		if out == "" {
			return "", errors.New("the model answered with no translation")
		}
		return out, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}
