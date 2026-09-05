// SPDX-License-Identifier: AGPL-3.0-only

package display

const (
	AnnotationDisplayName    = "networking.datumapis.com/display-name"
	AnnotationDisplayValue   = "networking.datumapis.com/display-value"
	AnnotationActivityChange = "networking.datumapis.com/activity-change"
	AnnotationActivityField  = "networking.datumapis.com/activity-field"
	AnnotationActivityName   = "networking.datumapis.com/activity-name"
	AnnotationActivityValue  = "networking.datumapis.com/activity-value"
	AnnotationChosenName     = "app.kubernetes.io/name"
)

const (
	ActivityChangeAdded   = "added"
	ActivityChangeRemoved = "removed"
	ActivityChangeUpdated = "updated"
)

const (
	ActivityFieldHostname   = "hostname"
	ActivityFieldBackend    = "backend"
	ActivityFieldRule       = "rule"
	ActivityFieldMode       = "mode"
	ActivityFieldExclusions = "exclusions"
	ActivityFieldSampling   = "sampling"
	ActivityFieldParanoia   = "paranoia"
)

type ActivityDiff struct {
	Change string
	Field  string
	Name   string
	Value  string
}

func stampDisplay(annotations map[string]string, name, value string) (map[string]string, bool) {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	changed := false
	if annotations[AnnotationDisplayName] != name {
		if name == "" {
			delete(annotations, AnnotationDisplayName)
		} else {
			annotations[AnnotationDisplayName] = name
		}
		changed = true
	}
	if annotations[AnnotationDisplayValue] != value {
		if value == "" {
			delete(annotations, AnnotationDisplayValue)
		} else {
			annotations[AnnotationDisplayValue] = value
		}
		changed = true
	}
	return annotations, changed
}

func stampActivity(annotations map[string]string, diff ActivityDiff) (map[string]string, bool) {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if diff.Change == "" {
		return clearActivity(annotations)
	}

	changed := false
	for key, want := range map[string]string{
		AnnotationActivityChange: diff.Change,
		AnnotationActivityField:  diff.Field,
		AnnotationActivityName:   diff.Name,
		AnnotationActivityValue:  diff.Value,
	} {
		if want == "" {
			if _, ok := annotations[key]; ok {
				delete(annotations, key)
				changed = true
			}
			continue
		}
		if annotations[key] != want {
			annotations[key] = want
			changed = true
		}
	}
	return annotations, changed
}

func clearActivity(annotations map[string]string) (map[string]string, bool) {
	if annotations == nil {
		return annotations, false
	}
	changed := false
	for _, key := range []string{
		AnnotationActivityChange,
		AnnotationActivityField,
		AnnotationActivityName,
		AnnotationActivityValue,
	} {
		if _, ok := annotations[key]; ok {
			delete(annotations, key)
			changed = true
		}
	}
	return annotations, changed
}
