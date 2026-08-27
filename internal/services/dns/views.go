package dns

import (
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) renderRecordsView() string {
	if s.selectedZone == nil {
		return "No zone selected"
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Zones",
		s.selectedZone.Name,
		"Records",
	))
	content.WriteString("\n\n")
	content.WriteString(s.recordTable.View())
	return content.String()
}
