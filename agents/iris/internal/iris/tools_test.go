package iris

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Fix bug 456", "fix-bug-456"},
		{"  Add OAuth Login flow  ", "add-oauth-login-flow"},
		{"!!!---!!!", "task"},
		{"", "task"},
		{"a-b-c", "a-b-c"},
		{"AVeryLongTitleThatGoesWellOverThe32CharacterCap", "averylongtitlethatgoeswelloverth"}, // capped at 32
	}
	for _, tc := range cases {
		got := slugify(tc.in)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripIrisPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"@iris what's running", "what's running"},
		{"  @iris status?", "status?"},
		{"@IRIS hello", "hello"},
		{"/iris status", "status"},
		{"/IRIS  show recent", "show recent"},
		{"@iris", ""},
		{"hello @iris", "hello @iris"}, // mention not at start
	}
	for _, tc := range cases {
		got := stripIrisPrefix(tc.in)
		if got != tc.want {
			t.Errorf("stripIrisPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitContent(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "okay, "},
		{Type: "tool_use", ID: "t1", Name: "query_state", Input: map[string]any{}},
		{Type: "text", Text: "checking..."},
	}
	tools, text := splitContent(blocks)
	if len(tools) != 1 || tools[0].ID != "t1" {
		t.Errorf("tools mismatch: %+v", tools)
	}
	if text != "okay, checking..." {
		t.Errorf("text: %q", text)
	}
}
