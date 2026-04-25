package discord

import (
	"strings"
	"testing"

	"github.com/zakros-hq/zakros/hermes/core"
)

func TestFormatMessageShape(t *testing.T) {
	cases := []struct {
		kind     core.MessageKind
		language string
		content  string
		mustHave []string
	}{
		{core.KindStatus, "", "working", []string{"working"}},
		{core.KindCode, "go", "fmt.Println()", []string{"```go", "fmt.Println()", "```"}},
		{core.KindThinking, "", "pondering", []string{"thinking", "pondering"}},
		{core.KindHuman, "", "need input", []string{"need input"}},
		{core.KindSummary, "", "all done", []string{"summary", "all done"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := formatMessage(core.Message{Kind: tc.kind, Language: tc.language, Content: tc.content})
			for _, sub := range tc.mustHave {
				if !strings.Contains(strings.ToLower(got), strings.ToLower(sub)) {
					t.Errorf("want substring %q in %q", sub, got)
				}
			}
		})
	}
}
