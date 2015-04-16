package canal

import (
	"fmt"
	"time"

	"github.com/siddontang/go-mysql/mysql"
	"github.com/siddontang/go-mysql/replication"
	"github.com/siddontang/go/log"
)

func (c *Canal) startSyncBinlog() error {
	pos := mysql.Position{c.master.Name, c.master.Position}

	log.Infof("start sync binlog at %v", pos)

	s, err := c.syncer.StartSync(pos)
	if err != nil {
		return fmt.Errorf("start sync replication at %v error %v", pos, err)
	}

	timeout := time.Second
	forceSavePos := false
	for {
		ev, err := s.GetEventTimeout(timeout)
		if err != nil && err != replication.ErrGetEventTimeout {
			return err
		} else if err == replication.ErrGetEventTimeout {
			timeout = 2 * timeout
			continue
		}

		timeout = time.Second

		//next binlog pos
		pos.Pos = ev.Header.LogPos
		c.master.Update(pos.Name, pos.Pos)

		forceSavePos = false

		switch e := ev.Event.(type) {
		case *replication.RotateEvent:
			pos.Name = string(e.NextLogName)
			pos.Pos = uint32(e.Position)
			// r.ev <- pos
			c.master.Update(pos.Name, pos.Pos)
			forceSavePos = true
			log.Infof("rotate binlog to %v", pos)
		case *replication.RowsEvent:
			// we only focus row based event
			if err = c.handleRowsEvent(ev); err != nil {
				log.Errorf("handle rows event error %v", err)
			}
		default:
		}

		c.master.Save(forceSavePos)
	}

	return nil
}

func (c *Canal) handleRowsEvent(e *replication.BinlogEvent) error {
	ev := e.Event.(*replication.RowsEvent)

	// Caveat: table may be altered at runtime.
	schema := string(ev.Table.Schema)
	table := string(ev.Table.Table)

	t, err := c.getTable(schema, table)
	if err != nil {
		return err
	}
	var action string
	switch e.Header.EventType {
	case replication.WRITE_ROWS_EVENTv1, replication.WRITE_ROWS_EVENTv2:
		action = InsertAction
	case replication.DELETE_ROWS_EVENTv1, replication.DELETE_ROWS_EVENTv2:
		action = DeleteAction
	case replication.UPDATE_ROWS_EVENTv1, replication.UPDATE_ROWS_EVENTv2:
		action = UpdateAction
	default:
		return fmt.Errorf("%s not supported now", e.Header.EventType)
	}
	events := newRowsEvent(t, action, ev.Rows)
	return c.travelRowsEventHandler(events)
}
