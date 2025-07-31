package types

type RowChange struct {
	Op         string
	Table      string
	Schema     string
	PrimaryKey string
	Before     map[string]any
	After      map[string]any
	LSN        string
	BinlogPos  string
}

type Sink interface {
	Upsert(id string, vector []float32, metadata map[string]any) error
	Delete(id string) error
	Close() error
}
