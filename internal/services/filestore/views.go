package filestore

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) renderDetailView() string {
	if s.selectedInstance == nil {
		return "No instance selected"
	}
	i := s.selectedInstance

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
		i.Name,
	)

	createTime := i.CreateTime
	if createTime == "" {
		createTime = "Unknown"
	}
	statusMsg := i.StatusMessage
	if statusMsg == "" {
		statusMsg = "-"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Instance Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: i.Name},
			{Key: "State", Value: components.RenderStatus(i.State)},
			{Key: "Status Message", Value: statusMsg},
			{Key: "Zone/Region", Value: i.Location},
			{Key: "Tier", Value: i.Tier},
			{Key: "Total Capacity", Value: fmt.Sprintf("%d GB", i.CapacityGB)},
			{Key: "Created", Value: createTime},
		},
	})

	network := components.DetailCard(components.DetailCardOpts{
		Title: "Network",
		Rows: []components.KeyValue{
			{Key: "VPC Network", Value: i.Network},
			{Key: "IP Addresses", Value: strings.Join(i.IPAddresses, ", ")},
		},
	})

	sections := []string{breadcrumb, "", card, "", network}

	if len(i.FileShares) > 0 {
		rows := make([]components.KeyValue, 0, len(i.FileShares))
		for _, fs := range i.FileShares {
			rows = append(rows, components.KeyValue{
				Key:   fs.Name,
				Value: fmt.Sprintf("%d GB", fs.CapacityGB),
			})
		}
		shares := components.DetailCard(components.DetailCardOpts{
			Title: "File Shares",
			Rows:  rows,
		})
		sections = append(sections, "", shares)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}
