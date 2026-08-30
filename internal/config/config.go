package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project  string              `yaml:"project"`
	Region   string              `yaml:"region"`
	Zone     string              `yaml:"zone"`
	UI       UIConfig            `yaml:"ui"`
	Features FeaturesConfig      `yaml:"features"`
	Projects []ConfiguredProject `yaml:"projects"`
	Jobs     JobsConfig          `yaml:"jobs"`
}

// JobsConfig controls retention for the persisted job-history log
// (~/.tgcp/jobs.json, see internal/core/jobs.go) shown in the Job History
// view. Both limits always apply together (a job is kept only if it
// satisfies both); either can be disabled independently by setting it <= 0.
type JobsConfig struct {
	MaxCount   int `yaml:"max_count"`    // 0/unset -> DefaultConfig's 500; <=0 (explicit) means unbounded by count
	MaxAgeDays int `yaml:"max_age_days"` // 0/unset -> DefaultConfig's 30; <=0 (explicit) means unbounded by age
}

// ConfiguredProject is one entry in the `projects:` list in ~/.tgcprc,
// letting a user define a fixed set of GCP projects to jump between with the
// quick project switcher (Ctrl+g) instead of paging through every project
// their account can see via the Cloud Resource Manager API.
type ConfiguredProject struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

type UIConfig struct {
	SidebarVisible  bool   `yaml:"sidebar_visible"`
	RefreshInterval int    `yaml:"refresh_interval"`
	DefaultView     string `yaml:"default_view"`
}

type FeaturesConfig struct {
	EnableGCE      bool `yaml:"enable_gce"`
	EnableCloudSQL bool `yaml:"enable_cloudsql"`
}

func DefaultConfig() *Config {
	return &Config{
		UI: UIConfig{
			SidebarVisible:  false,
			RefreshInterval: 30,
			DefaultView:     "home",
		},
		Features: FeaturesConfig{
			EnableGCE:      true,
			EnableCloudSQL: true,
		},
		Jobs: JobsConfig{
			MaxCount:   500,
			MaxAgeDays: 30,
		},
	}
}

func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, err
	}

	configPath := filepath.Join(home, ".tgcprc")
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return cfg, nil // Use defaults
	}
	if err != nil {
		return cfg, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}
