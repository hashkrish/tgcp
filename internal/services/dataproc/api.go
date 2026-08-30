package dataproc

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/dataproc/v1"
)

type Client struct {
	service *dataproc.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := dataproc.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("dataproc client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListClusters(projectID string, region string) ([]Cluster, error) {
	if demo.Enabled {
		return []Cluster{}, nil
	}
	var clusters []Cluster

	err := c.service.Projects.Regions.Clusters.List(projectID, region).Pages(context.Background(), func(page *dataproc.ListClustersResponse) error {
		for _, cl := range page.Clusters {
			status := "UNKNOWN"
			statusDetail := ""
			stateStartTime := ""
			if cl.Status != nil {
				status = cl.Status.State
				statusDetail = cl.Status.Detail
				stateStartTime = cl.Status.StateStartTime
			}

			configBucket := ""

			masterType := "N/A"
			workerType := "N/A"
			workerCount := 0
			zone := ""

			if cl.Config != nil {
				if cl.Config.MasterConfig != nil {
					masterType = machineTypeShort(cl.Config.MasterConfig.MachineTypeUri)
				}
				if cl.Config.WorkerConfig != nil {
					workerType = machineTypeShort(cl.Config.WorkerConfig.MachineTypeUri)
					workerCount = int(cl.Config.WorkerConfig.NumInstances)
				}
				if cl.Config.GceClusterConfig != nil {
					zone = machineTypeShort(cl.Config.GceClusterConfig.ZoneUri) // Reuse shortener for zone
				}
				configBucket = cl.Config.ConfigBucket
			}

			clusters = append(clusters, Cluster{
				Name:           cl.ClusterName,
				ProjectID:      projectID,
				Status:         status,
				MasterMachine:  masterType,
				WorkerCount:    workerCount,
				WorkerMachine:  workerType,
				Zone:           zone,
				ClusterUUID:    cl.ClusterUuid,
				StatusDetail:   statusDetail,
				StateStartTime: stateStartTime,
				ConfigBucket:   configBucket,
				Labels:         cl.Labels,
			})
		}
		return nil
	})
	return clusters, err
}

// CreateCluster creates a new Dataproc cluster in the given region.
func (c *Client) CreateCluster(projectID, region, clusterName, zone, masterMachineType, workerMachineType string, numWorkers int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}

	config := &dataproc.ClusterConfig{
		MasterConfig: &dataproc.InstanceGroupConfig{
			NumInstances:   1,
			MachineTypeUri: masterMachineType,
		},
		WorkerConfig: &dataproc.InstanceGroupConfig{
			NumInstances:   int64(numWorkers),
			MachineTypeUri: workerMachineType,
		},
	}
	if zone != "" {
		config.GceClusterConfig = &dataproc.GceClusterConfig{
			ZoneUri: zone,
		}
	}

	cluster := &dataproc.Cluster{
		ClusterName: clusterName,
		ProjectId:   projectID,
		Config:      config,
	}

	_, err := c.service.Projects.Regions.Clusters.Create(projectID, region, cluster).Do()
	return err
}

// UpdateClusterWorkerCount patches the primary worker-group node count of an
// existing cluster, matching `gcloud dataproc clusters update --num-workers`.
// Secondary/preemptible worker groups, autoscaling policies, and graceful
// decommission timeouts are out of scope for this minimal Update flow.
func (c *Client) UpdateClusterWorkerCount(projectID, region, clusterName string, numWorkers int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	cluster := &dataproc.Cluster{
		ClusterName: clusterName,
		ProjectId:   projectID,
		Config: &dataproc.ClusterConfig{
			WorkerConfig: &dataproc.InstanceGroupConfig{
				NumInstances: int64(numWorkers),
			},
		},
	}
	_, err := c.service.Projects.Regions.Clusters.Patch(projectID, region, clusterName, cluster).
		UpdateMask("config.worker_config.num_instances").Do()
	return err
}

// DeleteCluster deletes a Dataproc cluster, matching
// `gcloud dataproc clusters delete`.
func (c *Client) DeleteCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Delete(projectID, region, clusterName).Do()
	return err
}

// StartCluster starts a stopped Dataproc cluster, matching
// `gcloud dataproc clusters start`.
func (c *Client) StartCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Start(projectID, region, clusterName, &dataproc.StartClusterRequest{}).Do()
	return err
}

// StopCluster stops a running Dataproc cluster, matching
// `gcloud dataproc clusters stop`. `diagnose` (collecting a diagnostic
// tarball) is a separate, higher-complexity operation with async output
// handling and is intentionally out of scope for this pass.
func (c *Client) StopCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Stop(projectID, region, clusterName, &dataproc.StopClusterRequest{}).Do()
	return err
}

// DiagnoseCluster kicks off diagnostic collection on a cluster, matching
// `gcloud dataproc clusters diagnose`. Like every other lifecycle action in
// this package, this is fire-and-forget: the real API returns a
// long-running operation that eventually yields a diagnostic tarball URI,
// but polling that operation to completion is a different kind of feature
// (async result surfacing) than this app's mutating-call pattern anywhere
// else, so only kicking off the diagnosis is in scope here.
func (c *Client) DiagnoseCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Diagnose(projectID, region, clusterName, &dataproc.DiagnoseClusterRequest{}).Do()
	return err
}

// GetClusterIAMPolicy reads a cluster's current IAM policy, matching
// `gcloud dataproc clusters get-iam-policy`. Used as the "look before you
// grant" read step before AddClusterIAMBinding.
func (c *Client) GetClusterIAMPolicy(projectID, region, clusterName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("dataproc client not initialized")
	}
	resource := fmt.Sprintf("projects/%s/regions/%s/clusters/%s", projectID, region, clusterName)
	policy, err := c.service.Projects.Regions.Clusters.GetIamPolicy(resource, &dataproc.GetIamPolicyRequest{}).Do()
	if err != nil {
		return nil, fmt.Errorf("get cluster IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddClusterIAMBinding grants a role to a member on a Dataproc cluster,
// matching `gcloud dataproc clusters add-iam-policy-binding`. It fetches
// the current policy, merges the new binding into it, and writes the whole
// policy back -- this never drops any existing binding, unlike a raw
// set-iam-policy.
func (c *Client) AddClusterIAMBinding(projectID, region, clusterName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	resource := fmt.Sprintf("projects/%s/regions/%s/clusters/%s", projectID, region, clusterName)
	policy, err := c.service.Projects.Regions.Clusters.GetIamPolicy(resource, &dataproc.GetIamPolicyRequest{}).Do()
	if err != nil {
		return fmt.Errorf("get cluster IAM policy: %w", err)
	}
	found := false
	for _, b := range policy.Bindings {
		if b.Role != role {
			continue
		}
		found = true
		alreadyMember := false
		for _, m := range b.Members {
			if m == member {
				alreadyMember = true
				break
			}
		}
		if !alreadyMember {
			b.Members = append(b.Members, member)
		}
		break
	}
	if !found {
		policy.Bindings = append(policy.Bindings, &dataproc.Binding{Role: role, Members: []string{member}})
	}
	_, err = c.service.Projects.Regions.Clusters.SetIamPolicy(resource, &dataproc.SetIamPolicyRequest{Policy: policy}).Do()
	return err
}

// SubmitSparkJob submits a minimal Spark job to a cluster, matching the
// simplest form of `gcloud dataproc jobs submit spark --class --jars`. Other
// job types (Hadoop, Hive, Pig, PySpark, etc.) are out of scope for this
// minimal submit flow.
func (c *Client) SubmitSparkJob(projectID, region, clusterName, mainClass, jarURI string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	job := &dataproc.Job{
		Placement: &dataproc.JobPlacement{ClusterName: clusterName},
		SparkJob: &dataproc.SparkJob{
			MainClass:   mainClass,
			JarFileUris: []string{jarURI},
		},
	}
	_, err := c.service.Projects.Regions.Jobs.Submit(projectID, region, &dataproc.SubmitJobRequest{Job: job}).Do()
	return err
}

// ListJobs lists Dataproc jobs in a region, matching `gcloud dataproc jobs list`.
func (c *Client) ListJobs(projectID, region string) ([]JobInfo, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("dataproc client not initialized")
	}
	var out []JobInfo
	err := c.service.Projects.Regions.Jobs.List(projectID, region).Pages(context.Background(), func(resp *dataproc.ListJobsResponse) error {
		for _, j := range resp.Jobs {
			info := JobInfo{}
			if j.Reference != nil {
				info.ID = j.Reference.JobId
			}
			if j.Placement != nil {
				info.ClusterName = j.Placement.ClusterName
			}
			if j.Status != nil {
				info.State = j.Status.State
			}
			switch {
			case j.SparkJob != nil:
				info.Type = "Spark"
			case j.HadoopJob != nil:
				info.Type = "Hadoop"
			case j.HiveJob != nil:
				info.Type = "Hive"
			case j.PigJob != nil:
				info.Type = "Pig"
			case j.PysparkJob != nil:
				info.Type = "PySpark"
			default:
				info.Type = "Unknown"
			}
			out = append(out, info)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// KillJob cancels a running Dataproc job, matching `gcloud dataproc jobs kill`.
func (c *Client) KillJob(projectID, region, jobID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Jobs.Cancel(projectID, region, jobID, &dataproc.CancelJobRequest{}).Do()
	return err
}

// machineTypeShort extracts "n1-standard-4" from full URI
func machineTypeShort(uri string) string {
	// .../zones/us-central1-a/machineTypes/n1-standard-4
	// or .../zones/us-central1-a
	parts := parseURI(uri) // Simplified check
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return uri
}

func parseURI(uri string) []string {
	// Simple splitter
	return strings.Split(uri, "/")
}
