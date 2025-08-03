package postgres

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/mehmetymw/cdc2vec/internal/config"
	"github.com/mehmetymw/cdc2vec/internal/types"

	"go.uber.org/zap"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

type PostgresCDC struct {
	cfg       config.Config
	mappings  map[string]config.Mapping
	logger    *zap.Logger
	stopCh    chan struct{}
	conn      *pgconn.PgConn
	relations map[uint32]relation
	pending   []types.RowChange
}

type relation struct {
	id      uint32
	schema  string
	table   string
	columns []string
}

func New(cfg config.Config, mappings []config.Mapping, logger *zap.Logger) (*PostgresCDC, error) {
	logger.Info("Creating new PostgresCDC instance", 
		zap.Int("mappings_count", len(mappings)),
		zap.String("dsn", cfg.Source.Postgres.DSN),
		zap.String("publication", cfg.Source.Postgres.Publication),
		zap.String("slot", cfg.Source.Postgres.Slot))
	
	m := make(map[string]config.Mapping)
	for _, x := range mappings {
		m[x.Table] = x
		logger.Info("Added table mapping", 
			zap.String("table", x.Table),
			zap.String("id_column", x.IDColumn),
			zap.Strings("text_columns", x.TextColumns),
			zap.Strings("metadata_columns", x.MetadataColumns))
	}
	
	logger.Info("Available table mappings:", zap.Any("mappings", m))
	return &PostgresCDC{cfg: cfg, mappings: m, logger: logger, stopCh: make(chan struct{}), relations: make(map[uint32]relation)}, nil
}

func (p *PostgresCDC) Run(out chan<- types.RowChange) {
	p.logger.Info("Starting PostgresCDC replication")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-p.stopCh
		p.logger.Info("Stop signal received, canceling context")
		cancel()
	}()
	for {
		if err := p.run(ctx, out); err != nil {
			p.logger.Error("Replication failed, retrying in 5s", zap.Error(err))
			select {
			case <-time.After(5 * time.Second):
				p.logger.Info("Retrying replication after 5s delay")
			case <-ctx.Done():
				p.logger.Info("Context canceled, stopping replication")
				return
			}
			continue
		}
		p.logger.Info("Replication completed successfully")
		return
	}
}

func (p *PostgresCDC) Stop() {
	p.logger.Info("Stopping PostgresCDC")
	select {
	case <-p.stopCh:
		p.logger.Debug("Stop channel already closed")
	default:
		close(p.stopCh)
		p.logger.Info("Stop signal sent")
	}
}

func (p *PostgresCDC) run(ctx context.Context, out chan<- types.RowChange) error {
	p.logger.Debug("Parsing PostgreSQL connection config")
	cfg, err := pgconn.ParseConfig(p.cfg.Source.Postgres.DSN)
	if err != nil {
		p.logger.Error("Failed to parse DSN", zap.Error(err))
		return err
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["replication"] = "database"
	
	p.logger.Info("Connecting to PostgreSQL for replication", 
		zap.String("host", cfg.Host),
		zap.Uint16("port", cfg.Port),
		zap.String("database", cfg.Database),
		zap.String("user", cfg.User))
	
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		p.logger.Error("Failed to connect to PostgreSQL", zap.Error(err))
		return err
	}
	p.conn = conn
	defer func() {
		p.logger.Debug("Closing PostgreSQL connection")
		conn.Close(ctx)
	}()

	if p.cfg.Source.Postgres.Publication != "" && p.cfg.Source.Postgres.CreatePublication {
		p.logger.Info("Creating publication", zap.String("publication", p.cfg.Source.Postgres.Publication))
		std, err := pgx.Connect(ctx, p.cfg.Source.Postgres.DSN)
		if err == nil {
			pub := p.cfg.Source.Postgres.Publication
			_, err := std.Exec(ctx, "CREATE PUBLICATION "+pub+" FOR ALL TABLES")
			if err != nil {
				p.logger.Warn("Failed to create publication (may already exist)", 
					zap.String("publication", pub), zap.Error(err))
			} else {
				p.logger.Info("Publication created successfully", zap.String("publication", pub))
			}
			std.Close(ctx)
		} else {
			p.logger.Error("Failed to connect for publication creation", zap.Error(err))
		}
	}
	if p.cfg.Source.Postgres.Slot != "" && p.cfg.Source.Postgres.CreateSlot {
		p.logger.Info("Creating replication slot", zap.String("slot", p.cfg.Source.Postgres.Slot))
		_, err := pglogrepl.CreateReplicationSlot(ctx, conn, p.cfg.Source.Postgres.Slot, "pgoutput", pglogrepl.CreateReplicationSlotOptions{})
		if err != nil {
			p.logger.Warn("Failed to create replication slot (may already exist)", 
				zap.String("slot", p.cfg.Source.Postgres.Slot), zap.Error(err))
		} else {
			p.logger.Info("Replication slot created successfully", zap.String("slot", p.cfg.Source.Postgres.Slot))
		}
	}

	startLSN := pglogrepl.LSN(0)
	if p.cfg.Source.Postgres.StartLSN != "" {
		p.logger.Debug("Parsing start LSN", zap.String("start_lsn", p.cfg.Source.Postgres.StartLSN))
		var hi, lo uint32
		fmt.Sscanf(p.cfg.Source.Postgres.StartLSN, "%X/%X", &hi, &lo)
		startLSN = pglogrepl.LSN(uint64(hi)<<32 | uint64(lo))
	}
	opts := pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", p.cfg.Source.Postgres.Publication),
		},
	}
	
	p.logger.Info("Starting replication", 
		zap.String("slot", p.cfg.Source.Postgres.Slot),
		zap.String("publication", p.cfg.Source.Postgres.Publication),
		zap.String("start_lsn", startLSN.String()))
	
	if err := pglogrepl.StartReplication(ctx, conn, p.cfg.Source.Postgres.Slot, startLSN, opts); err != nil {
		p.logger.Error("Failed to start replication", zap.Error(err))
		return err
	}
	p.logger.Info("Started PostgreSQL replication", zap.String("lsn", startLSN.String()))

	statusLSN := startLSN
	deadline := time.Now().Add(10 * time.Second)

	for {
		if err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, pglogrepl.StandbyStatusUpdate{WALWritePosition: statusLSN}); err != nil {
			p.logger.Error("Failed to send standby status update", zap.Error(err))
			return err
		}
		ctxR, cancel := context.WithDeadline(ctx, deadline)
		msg, err := conn.ReceiveMessage(ctxR)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) {
				p.logger.Debug("Receive message timeout, continuing")
				deadline = time.Now().Add(10 * time.Second)
				continue
			}
			p.logger.Error("Failed to receive message", zap.Error(err))
			return err
		}
		if msg, ok := msg.(*pgproto3.CopyData); ok {
			if len(msg.Data) == 0 {
				p.logger.Debug("Received empty copy data, skipping")
				continue
			}
			switch msg.Data[0] {
			case 'w':
				p.logger.Debug("Received WAL data message")
				x, err := pglogrepl.ParseXLogData(msg.Data[1:])
				if err != nil {
					p.logger.Error("Failed to parse XLog data", zap.Error(err))
					return err
				}
				p.logger.Debug("Calling handleXLog", zap.Int("data_size", len(x.WALData)))
				p.handleXLog(out, x.WALData)
				statusLSN = x.WALStart
				p.logger.Info("Updated status LSN", zap.String("lsn", statusLSN.String()))
			case 'k':
				//skip for now
			default:
				p.logger.Debug("Received unknown message type", zap.Uint8("type", msg.Data[0]))
			}
		}
	}
}

func (p *PostgresCDC) handleXLog(out chan<- types.RowChange, data []byte) {
	p.logger.Debug("Processing XLog data", zap.Int("data_size", len(data)))
	
	// Use pglogrepl's built-in parsing
	logicalMsg, err := pglogrepl.Parse(data)
	if err != nil {
		p.logger.Error("Failed to parse logical message", zap.Error(err))
		return
	}
	
	switch msg := logicalMsg.(type) {
	case *pglogrepl.RelationMessage:
		p.logger.Debug("Processing Relation message")
		rel := relation{
			id:      msg.RelationID,
			schema:  msg.Namespace,
			table:   msg.RelationName,
			columns: make([]string, len(msg.Columns)),
		}
		for i, col := range msg.Columns {
			rel.columns[i] = col.Name
		}
		p.relations[rel.id] = rel
		p.logger.Debug("Added relation", 
			zap.Uint32("id", rel.id),
			zap.String("schema", rel.schema),
			zap.String("table", rel.table),
			zap.Strings("columns", rel.columns))
	case *pglogrepl.InsertMessage:
		p.logger.Debug("Processing Insert message")
		change := p.parseInsertMessage(msg)
		p.pending = append(p.pending, change)
		p.logger.Info("Added insert change", 
			zap.String("primary_key", change.PrimaryKey),
			zap.Any("available_mappings", p.getTableNames()))
	case *pglogrepl.UpdateMessage:
		p.logger.Debug("Processing Update message")
		change := p.parseUpdateMessage(msg)
		p.pending = append(p.pending, change)
		p.logger.Info("Added update change", 
			zap.String("table", change.Schema+"."+change.Table),
			zap.String("primary_key", change.PrimaryKey),
			zap.Any("available_mappings", p.getTableNames()))
	case *pglogrepl.DeleteMessage:
		p.logger.Debug("Processing Delete message")
		change := p.parseDeleteMessage(msg)
		p.pending = append(p.pending, change)
		p.logger.Info("Added delete change", 
			zap.String("table", change.Schema+"."+change.Table),
			zap.String("primary_key", change.PrimaryKey),
			zap.Any("available_mappings", p.getTableNames()))
	case *pglogrepl.CommitMessage:
		p.logger.Debug("Processing Commit message")
		lsn := pglogrepl.LSN(msg.CommitLSN).String()
		p.logger.Info("Committing transaction", 
			zap.String("lsn", lsn),
			zap.Int("pending_changes", len(p.pending)))
		
		sent := 0
		for i := range p.pending {
			p.pending[i].LSN = lsn
			tableName := p.pending[i].Schema + "." + p.pending[i].Table
			
			// Check if we have mapping for this table
			if _, exists := p.mappings[tableName]; exists {
				p.logger.Info("Sending change to pipeline", 
					zap.String("table", tableName),
					zap.String("op", p.pending[i].Op),
					zap.String("primary_key", p.pending[i].PrimaryKey))
				
				select {
				case out <- p.pending[i]:
					sent++
					p.logger.Info("Change sent successfully to pipeline", 
						zap.String("table", tableName),
						zap.String("op", p.pending[i].Op))
				default:
					p.logger.Error("Output channel full, dropping change", 
						zap.String("table", tableName))
				}
			} else {
				p.logger.Info("No mapping for table, skipping", 
					zap.String("table", tableName))
			}
		}
		
		p.logger.Info("Transaction committed", 
			zap.String("lsn", lsn),
			zap.Int("total_changes", len(p.pending)),
			zap.Int("sent_changes", sent))
		
		p.pending = nil
	default:
		p.logger.Debug("Unknown message type, skipping", zap.String("type", fmt.Sprintf("%T", msg)))
	}
}



func (p *PostgresCDC) parseRelation(r *bytes.Reader) relation {
	id := readUint32(r)
	schema := readPgString(r)
	table := readPgString(r)
	readByte(r)
	ncols := int(readUint16(r))
	cols := make([]string, ncols)
	for i := 0; i < ncols; i++ {
		readByte(r)
		readUint32(r)
		readInt32(r)
		name := readPgString(r)
		cols[i] = name
	}
	p.logger.Debug("Parsed relation", 
		zap.Uint32("id", id),
		zap.String("schema", schema),
		zap.String("table", table),
		zap.Strings("columns", cols))
	return relation{id: id, schema: schema, table: table, columns: cols}
}

func (p *PostgresCDC) parseTuple(r *bytes.Reader, cols []string) map[string]any {
	n := int(readUint16(r))
	m := make(map[string]any, n)
	p.logger.Debug("Parsing tuple", 
		zap.Int("num_columns", n),
		zap.Strings("column_names", cols))
	
	for i := 0; i < n && i < len(cols); i++ {
		tag := readByte(r)
		p.logger.Debug("Processing column", 
			zap.Int("index", i),
			zap.String("column", cols[i]),
			zap.Uint8("tag", tag),
			zap.String("tag_char", string(rune(tag))))
		
		switch tag {
		case 'n':
			m[cols[i]] = nil
			p.logger.Debug("Column is null", zap.String("column", cols[i]))
		case 'u':
			m[cols[i]] = nil
			p.logger.Debug("Column is unchanged", zap.String("column", cols[i]))
		case 't':
			l := int(readUint32(r))
			buf := make([]byte, l)
			r.Read(buf)
			value := string(buf)
			m[cols[i]] = value
			p.logger.Debug("Column value", 
				zap.String("column", cols[i]),
				zap.Int("length", l),
				zap.String("value", value))
		default:
			m[cols[i]] = nil
			p.logger.Debug("Unknown tag, setting to nil", 
				zap.String("column", cols[i]),
				zap.Uint8("tag", tag))
		}
	}
	p.logger.Debug("Parsed tuple result", zap.Any("data", m))
	return m
}

func (p *PostgresCDC) parseInsert(r *bytes.Reader) types.RowChange {
	relid := readUint32(r)
	rel := p.relations[relid]
	readByte(r)
	after := p.parseTuple(r, rel.columns)
	
	tableName := rel.schema + "." + rel.table
	mp, exists := p.mappings[tableName]
	var pk string
	if exists && after != nil {
		if pkVal, ok := after[mp.IDColumn]; ok {
			pk = fmt.Sprintf("%v", pkVal)
		}
	}
	
	p.logger.Info("Parsed insert with details", 
		zap.Uint32("relation_id", relid),
		zap.String("table", tableName),
		zap.String("primary_key", pk),
		zap.Bool("has_mapping", exists),
		zap.String("id_column", func() string {
			if exists {
				return mp.IDColumn
			}
			return "NO_MAPPING"
		}()),
		zap.Strings("available_columns", rel.columns),
		zap.Any("after_data", after))
	
	return types.RowChange{Op: "c", Schema: rel.schema, Table: rel.table, PrimaryKey: pk, After: after}
}

func (p *PostgresCDC) parseUpdate(r *bytes.Reader) types.RowChange {
	relid := readUint32(r)
	rel := p.relations[relid]
	tag := readByte(r)
	if tag == 'K' || tag == 'O' {
		_ = p.parseTuple(r, rel.columns)
		tag = readByte(r)
	}
	if tag == 'N' {
	}
	after := p.parseTuple(r, rel.columns)
	
	tableName := rel.schema + "." + rel.table
	mp, exists := p.mappings[tableName]
	var pk string
	if exists && after != nil {
		if pkVal, ok := after[mp.IDColumn]; ok {
			pk = fmt.Sprintf("%v", pkVal)
		}
	}
	
	p.logger.Debug("Parsed update", 
		zap.Uint32("relation_id", relid),
		zap.String("table", tableName),
		zap.String("primary_key", pk),
		zap.Bool("has_mapping", exists),
		zap.Any("after", after))
	
	return types.RowChange{Op: "u", Schema: rel.schema, Table: rel.table, PrimaryKey: pk, After: after}
}

func (p *PostgresCDC) parseDelete(r *bytes.Reader) types.RowChange {
	relid := readUint32(r)
	rel := p.relations[relid]
	tag := readByte(r)
	var before map[string]any
	if tag == 'K' || tag == 'O' {
		before = p.parseTuple(r, rel.columns)
	}
	
	tableName := rel.schema + "." + rel.table
	mp, exists := p.mappings[tableName]
	var pk string
	if exists && before != nil {
		if pkVal, ok := before[mp.IDColumn]; ok {
			pk = fmt.Sprintf("%v", pkVal)
		}
	}
	
	p.logger.Debug("Parsed delete", 
		zap.Uint32("relation_id", relid),
		zap.String("table", tableName),
		zap.String("primary_key", pk),
		zap.Bool("has_mapping", exists),
		zap.Any("before", before))
	
	return types.RowChange{Op: "d", Schema: rel.schema, Table: rel.table, PrimaryKey: pk, Before: before}
}

func readByte(r *bytes.Reader) byte {
	var b [1]byte
	r.Read(b[:])
	return b[0]
}

func readUint16(r *bytes.Reader) uint16 {
	var v uint16
	binary.Read(r, binary.BigEndian, &v)
	return v
}

func readUint32(r *bytes.Reader) uint32 {
	var v uint32
	binary.Read(r, binary.BigEndian, &v)
	return v
}

func readInt32(r *bytes.Reader) int32 {
	var v int32
	binary.Read(r, binary.BigEndian, &v)
	return v
}

func readUint64(r *bytes.Reader) uint64 {
	var v uint64
	binary.Read(r, binary.BigEndian, &v)
	return v
}

func readPgString(r *bytes.Reader) string {
	// PostgreSQL strings in logical replication are null-terminated
	var buf []byte
	var rawBytes []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			break
		}
		rawBytes = append(rawBytes, b)
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	result := string(buf)
	// Debug log to see what we're getting
	if len(rawBytes) > 0 {
		fmt.Printf("DEBUG: Raw bytes: %v, ASCII: %q, Result: %q\n", rawBytes, string(rawBytes[:len(rawBytes)-1]), result)
	}
	return result
}

func readString(r *bytes.Reader) string {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil || b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}

func readCString(r *bytes.Reader) string {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil || b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}


func (p *PostgresCDC) parseInsertMessage(msg *pglogrepl.InsertMessage) types.RowChange {
	rel := p.relations[msg.RelationID]
	after := p.parseTupleData(msg.Tuple, rel.columns)
	
	tableName := rel.schema + "." + rel.table
	mp, exists := p.mappings[tableName]
	var pk string
	if exists && after != nil {
		if pkVal, ok := after[mp.IDColumn]; ok {
			pk = fmt.Sprintf("%v", pkVal)
		}
	}
	
	p.logger.Info("Parsed insert with details", 
		zap.Uint32("relation_id", msg.RelationID),
		zap.String("table", tableName),
		zap.String("primary_key", pk),
		zap.Bool("has_mapping", exists),
		zap.String("id_column", func() string {
			if exists {
				return mp.IDColumn
			}
			return "NO_MAPPING"
		}()),
		zap.Strings("available_columns", rel.columns),
		zap.Any("after_data", after))
	
	return types.RowChange{Op: "c", Schema: rel.schema, Table: rel.table, PrimaryKey: pk, After: after}
}

func (p *PostgresCDC) parseUpdateMessage(msg *pglogrepl.UpdateMessage) types.RowChange {
	rel := p.relations[msg.RelationID]
	after := p.parseTupleData(msg.NewTuple, rel.columns)
	
	tableName := rel.schema + "." + rel.table
	mp, exists := p.mappings[tableName]
	var pk string
	if exists && after != nil {
		if pkVal, ok := after[mp.IDColumn]; ok {
			pk = fmt.Sprintf("%v", pkVal)
		}
	}
	
	return types.RowChange{Op: "u", Schema: rel.schema, Table: rel.table, PrimaryKey: pk, After: after}
}

func (p *PostgresCDC) parseDeleteMessage(msg *pglogrepl.DeleteMessage) types.RowChange {
	rel := p.relations[msg.RelationID]
	var before map[string]any
	if msg.OldTuple != nil {
		before = p.parseTupleData(msg.OldTuple, rel.columns)
	}
	
	tableName := rel.schema + "." + rel.table
	mp, exists := p.mappings[tableName]
	var pk string
	if exists && before != nil {
		if pkVal, ok := before[mp.IDColumn]; ok {
			pk = fmt.Sprintf("%v", pkVal)
		}
	}
	
	return types.RowChange{Op: "d", Schema: rel.schema, Table: rel.table, PrimaryKey: pk, Before: before}
}

func (p *PostgresCDC) parseTupleData(tuple *pglogrepl.TupleData, columns []string) map[string]any {
	if tuple == nil {
		return nil
	}
	
	result := make(map[string]any)
	for i, col := range tuple.Columns {
		if i < len(columns) {
			switch col.DataType {
			case 'n':
				result[columns[i]] = nil
			case 'u':
				result[columns[i]] = nil
			case 't':
				result[columns[i]] = string(col.Data)
			default:
				result[columns[i]] = nil
			}
		}
	}
	
	p.logger.Debug("Parsed tuple data", 
		zap.Strings("columns", columns),
		zap.Any("result", result))
	
	return result
}

func (p *PostgresCDC) getTableNames() []string {
	var tables []string
	for table := range p.mappings {
		tables = append(tables, table)
	}
	return tables
}
