package bigquery

import (
	"context"
	"fmt"

	"cloud.google.com/go/bigquery"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/iterator"
)

type Client struct {
	client *bigquery.Client
}

func NewClient(ctx context.Context, projectID string) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	c, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create bigquery client: %w", err)
	}
	return &Client{client: c}, nil
}

func (c *Client) ListDatasets(projectID string) ([]Dataset, error) {
	if demo.Enabled {
		return []Dataset{}, nil
	}
	var datasets []Dataset
	it := c.client.Datasets(context.Background())
	for {
		ds, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		// Fetch metadata for Location
		md, err := ds.Metadata(context.Background())
		if err != nil {
			// If error fetching metadata, we can still list ID?
			// Or just skip/log? Let's just list ID and unknown location
			datasets = append(datasets, Dataset{
				ID:        ds.DatasetID,
				ProjectID: ds.ProjectID,
				Location:  "UNKNOWN",
			})
			continue
		}

		datasets = append(datasets, Dataset{
			ID:           ds.DatasetID,
			ProjectID:    ds.ProjectID,
			Location:     md.Location,
			Description:  md.Description,
			Labels:       md.Labels,
			CreationTime: md.CreationTime,
		})
	}
	return datasets, nil
}

// CreateDataset creates a new BigQuery dataset with the given ID and location.
func (c *Client) CreateDataset(datasetID, location string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("bigquery client not initialized")
	}
	meta := &bigquery.DatasetMetadata{
		Location: location,
	}
	return c.client.Dataset(datasetID).Create(context.Background(), meta)
}

// UpdateDatasetDescription patches a dataset's description, matching
// `bq update --description`. Other update fields (labels, access, default
// table expiration, etc.) are out of scope for this minimal Update flow.
func (c *Client) UpdateDatasetDescription(datasetID, description string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("bigquery client not initialized")
	}
	update := bigquery.DatasetMetadataToUpdate{
		Description: description,
	}
	_, err := c.client.Dataset(datasetID).Update(context.Background(), update, "")
	return err
}

// DeleteDataset deletes a BigQuery dataset, matching `bq rm -d` (without
// -f/--recursive) — the API itself refuses to delete a non-empty dataset,
// which is the safety behavior this app wants (no table-level delete is
// implemented here to empty it first).
func (c *Client) DeleteDataset(datasetID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("bigquery client not initialized")
	}
	return c.client.Dataset(datasetID).Delete(context.Background())
}

func (c *Client) ListTables(datasetID string) ([]Table, error) {
	if demo.Enabled {
		return []Table{}, nil
	}
	var tables []Table
	ds := c.client.Dataset(datasetID)
	it := ds.Tables(context.Background())
	for {
		t, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		// We need to fetch metadata to get NumRows etc.
		// NOTE: Fetching metadata for ALL tables might be slow properly.
		// For MVP, we might skip detailed metadata in the list or do parallel fetch?
		// Let's just return basic info for list and maybe fetch details strictly if needed.
		// Actually, `t` is *Table. The iterator returns basic info.
		// Let's assume for now we just want IDs.
		// Wait, user wants drill down.
		// Let's fetch metadata for the *Table* to get type.

		md, err := t.Metadata(context.Background())
		if err != nil {
			// Skip or handle? Let's just log or ignore?
			// Better to show it with unknown data.
			tables = append(tables, Table{
				ID:        t.TableID,
				DatasetID: datasetID,
				Type:      "UNKNOWN",
			})
			continue
		}

		partitioning := ""
		if md.TimePartitioning != nil {
			partitioning = string(md.TimePartitioning.Type)
			if md.TimePartitioning.Field != "" {
				partitioning = fmt.Sprintf("%s (%s)", partitioning, md.TimePartitioning.Field)
			}
		} else if md.RangePartitioning != nil {
			partitioning = fmt.Sprintf("RANGE (%s)", md.RangePartitioning.Field)
		}

		tables = append(tables, Table{
			ID:             t.TableID,
			DatasetID:      datasetID,
			Type:           string(md.Type),
			NumRows:        md.NumRows,
			TotalBytes:     md.NumBytes,
			LastMod:        md.LastModifiedTime,
			Description:    md.Description,
			CreationTime:   md.CreationTime,
			ExpirationTime: md.ExpirationTime,
			Partitioning:   partitioning,
		})
	}
	return tables, nil
}

// CreateTable creates a new table in a dataset with the given schema,
// matching `bq mk --table dataset.table field:type,field:type`.
func (c *Client) CreateTable(datasetID, tableID string, schema []SchemaField) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("bigquery client not initialized")
	}
	bqSchema := make(bigquery.Schema, 0, len(schema))
	for _, f := range schema {
		bqSchema = append(bqSchema, &bigquery.FieldSchema{
			Name:     f.Name,
			Type:     bigquery.FieldType(f.Type),
			Required: f.Mode == "REQUIRED",
			Repeated: f.Mode == "REPEATED",
		})
	}
	return c.client.Dataset(datasetID).Table(tableID).Create(context.Background(), &bigquery.TableMetadata{Schema: bqSchema})
}

// DeleteTable deletes a single table, matching `bq rm -t dataset.table`.
func (c *Client) DeleteTable(datasetID, tableID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("bigquery client not initialized")
	}
	return c.client.Dataset(datasetID).Table(tableID).Delete(context.Background())
}

// RunQuery executes an arbitrary SQL statement (SELECT, INSERT, UPDATE,
// CREATE TABLE AS SELECT, etc.) as a BigQuery job and returns up to
// queryResultRowLimit rows of its result, matching `bq query`. This is the
// data-plane entry point for insert/show-rows/copy/jobs -- every BigQuery
// query already runs as a job, and INSERT/CTAS statements are themselves
// valid SQL, so one query runner covers all four rather than needing
// separate insert/copy/jobs-list flows.
func (c *Client) RunQuery(projectID, sql string) (*QueryResult, error) {
	if demo.Enabled {
		return &QueryResult{Columns: []string{"col"}, Rows: [][]string{{"demo"}}, RowCount: 1}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("bigquery client not initialized")
	}
	ctx := context.Background()
	q := c.client.Query(sql)
	it, err := q.Read(ctx)
	if err != nil {
		return nil, err
	}

	result := &QueryResult{}
	for _, f := range it.Schema {
		result.Columns = append(result.Columns, f.Name)
	}

	for {
		var row []bigquery.Value
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		result.RowCount++
		if len(result.Rows) >= queryResultRowLimit {
			result.Truncated = true
			continue
		}
		strRow := make([]string, len(row))
		for i, v := range row {
			strRow[i] = fmt.Sprintf("%v", v)
		}
		result.Rows = append(result.Rows, strRow)
	}
	if len(result.Columns) == 0 {
		result.Columns = []string{"result"}
		result.Rows = [][]string{{"query completed (no rows returned)"}}
	}
	return result, nil
}

func (c *Client) GetTableSchema(datasetID, tableID string) ([]SchemaField, error) {
	if demo.Enabled {
		return []SchemaField{}, nil
	}
	ds := c.client.Dataset(datasetID)
	t := ds.Table(tableID)
	md, err := t.Metadata(context.Background())
	if err != nil {
		return nil, err
	}

	var fields []SchemaField
	for _, f := range md.Schema {
		fields = append(fields, SchemaField{
			Name:        f.Name,
			Type:        string(f.Type),
			Mode:        "", // Will be populated shortly
			Description: f.Description,
		})
	}
	// Re-map mode correctly
	for i, f := range md.Schema {
		mode := "NULLABLE"
		if f.Required {
			mode = "REQUIRED"
		} else if f.Repeated {
			mode = "REPEATED"
		}
		fields[i].Mode = mode
	}

	return fields, nil
}
