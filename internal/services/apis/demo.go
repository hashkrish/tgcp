package apis

import "github.com/yogirk/tgcp/internal/demo"

func loadDemoAPIs(enabledOnly bool) []API {
	var fixtures []API
	demo.MustLoad("apis", &fixtures)

	out := make([]API, 0, len(fixtures))
	for _, f := range fixtures {
		if enabledOnly && f.State != "ENABLED" {
			continue
		}
		out = append(out, f)
	}
	sortAPIs(out)
	return out
}
