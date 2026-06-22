package downstreamclient

import "testing"

func TestUpstreamClusterNameFromLabel(t *testing.T) {
	cases := []struct {
		name  string
		label string
		want  string
	}{
		{
			name:  "modern slash-less name",
			label: "cluster-my-project-abc123",
			want:  "my-project-abc123",
		},
		{
			name:  "legacy leading-slash name (pre-#196)",
			label: "cluster-_my-project-abc123",
			want:  "my-project-abc123",
		},
		{
			name:  "multi-segment name is preserved",
			label: "cluster-org_my-project",
			want:  "org/my-project",
		},
		{
			name:  "empty label",
			label: "",
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UpstreamClusterNameFromLabel(tc.label); got != tc.want {
				t.Errorf("UpstreamClusterNameFromLabel(%q) = %q, want %q", tc.label, got, tc.want)
			}
		})
	}
}
