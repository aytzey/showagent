package importer

import "fmt"

// Summary describes source losses without rendering an untrusted warning code.
func (warning Warning) Summary() string {
	count := max(0, warning.Count)
	switch warning.Code {
	case "omitted_non_text_content":
		return fmt.Sprintf("%d non-text content blocks omitted", count)
	case "omitted_unsupported_roles":
		return fmt.Sprintf("%d messages with unsupported roles omitted", count)
	default:
		return "The source reports an additional import warning"
	}
}
