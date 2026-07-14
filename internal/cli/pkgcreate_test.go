package cli

import (
	"strings"
	"testing"
)

func TestAdjustPassthrough(t *testing.T) {
	tests := []struct {
		name     string
		in       []string
		want     []string
		wantNote bool
	}{
		{
			// The whole point: `ros2 pkg create foo` from a workspace root makes
			// ./foo, colcon builds it anyway, and nothing tells you it is wrong.
			name:     "pkg create gets src/ by default",
			in:       []string{"pkg", "create", "my_pkg"},
			want:     []string{"pkg", "create", "my_pkg", destFlag, "src"},
			wantNote: true,
		},
		{
			name:     "with other flags",
			in:       []string{"pkg", "create", "--build-type", "ament_python", "my_pkg"},
			want:     []string{"pkg", "create", "--build-type", "ament_python", "my_pkg", destFlag, "src"},
			wantNote: true,
		},
		{
			// If the user said where to put it, we do not argue.
			name: "an explicit destination is respected",
			in:   []string{"pkg", "create", destFlag, ".", "my_pkg"},
			want: []string{"pkg", "create", destFlag, ".", "my_pkg"},
		},
		{
			name: "an explicit destination in --flag=value form is respected",
			in:   []string{"pkg", "create", destFlag + "=/tmp/elsewhere", "my_pkg"},
			want: []string{"pkg", "create", destFlag + "=/tmp/elsewhere", "my_pkg"},
		},
		{
			name: "help is left alone",
			in:   []string{"pkg", "create", "--help"},
			want: []string{"pkg", "create", "--help"},
		},
		{
			// Everything that is not `pkg create` must stay untouched. This is
			// the invariant the exception is carved out of.
			name: "pkg list is untouched",
			in:   []string{"pkg", "list"},
			want: []string{"pkg", "list"},
		},
		{
			name: "topic pub is untouched",
			in:   []string{"topic", "pub", "--once", "/c", "std_msgs/String"},
			want: []string{"topic", "pub", "--once", "/c", "std_msgs/String"},
		},
		{
			name: "run is untouched",
			in:   []string{"run", "my_pkg", "my_node"},
			want: []string{"run", "my_pkg", "my_node"},
		},
		{
			name: "a bare pkg is untouched",
			in:   []string{"pkg"},
			want: []string{"pkg"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, note := adjustPassthrough(tc.in)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("args = %v\n want %v", got, tc.want)
			}
			if (note != "") != tc.wantNote {
				t.Errorf("note = %q, wantNote = %v", note, tc.wantNote)
			}
		})
	}
}

// Rewriting someone's command without telling them is worse than the mistake
// being prevented.
func TestAdjustPassthrough_AlwaysExplainsItself(t *testing.T) {
	_, note := adjustPassthrough([]string{"pkg", "create", "my_pkg"})
	if note == "" {
		t.Fatal("the command was rewritten with no explanation")
	}
	if !strings.Contains(note, destFlag) {
		t.Errorf("note = %q, should name the flag so the user can override", note)
	}
}
