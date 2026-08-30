package cloudrun

import (
	"time"

	"github.com/yogirk/tgcp/internal/demo"
)

type demoRevision struct {
	Name           string `json:"name"`
	Percent        int64  `json:"percent"`
	Latest         bool   `json:"latest"`
	Tag            string `json:"tag"`
	Image          string `json:"image"`
	CreatedDaysAgo int    `json:"createdDaysAgo"`
}

type demoRunService struct {
	Name                string         `json:"name"`
	Region              string         `json:"region"`
	URL                 string         `json:"url"`
	Status              string         `json:"status"`
	LastModifiedDaysAgo int            `json:"lastModifiedDaysAgo"`
	Revisions           []demoRevision `json:"revisions"`
}

// demoRevisionsByService caches the fixture file's revision lists, keyed by
// service name, so loadDemoRevisions (called per service, on entering its
// detail view) doesn't need to re-parse the fixture file every time.
var demoRevisionsByService map[string][]demoRevision

func loadDemoRevisions(serviceName string) []Revision {
	if demoRevisionsByService == nil {
		var fixtures []demoRunService
		demo.MustLoad("cloudrun", &fixtures)
		demoRevisionsByService = make(map[string][]demoRevision, len(fixtures))
		for _, f := range fixtures {
			demoRevisionsByService[f.Name] = f.Revisions
		}
	}

	now := time.Now()
	revs := demoRevisionsByService[serviceName]
	out := make([]Revision, len(revs))
	for i, r := range revs {
		out[i] = Revision{
			Name:    r.Name,
			Percent: r.Percent,
			Latest:  r.Latest,
			Tag:     r.Tag,
			Image:   r.Image,
			Created: now.AddDate(0, 0, -r.CreatedDaysAgo),
		}
	}
	return out
}

func loadDemoRunServices() []RunService {
	var fixtures []demoRunService
	demo.MustLoad("cloudrun", &fixtures)

	now := time.Now()
	out := make([]RunService, len(fixtures))
	for i, f := range fixtures {
		var revisions []Revision
		latestReady := ""
		for _, r := range f.Revisions {
			revisions = append(revisions, Revision{
				Name:    r.Name,
				Percent: r.Percent,
				Latest:  r.Latest,
				Tag:     r.Tag,
			})
			if r.Latest {
				latestReady = r.Name
			}
		}
		out[i] = RunService{
			Name:                f.Name,
			Region:              f.Region,
			URL:                 f.URL,
			Status:              ServiceStatus(f.Status),
			LastModified:        now.AddDate(0, 0, -f.LastModifiedDaysAgo),
			Revisions:           revisions,
			LatestReadyRevision: latestReady,
		}
	}
	return out
}
