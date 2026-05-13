package entity

import "time"

type Record struct {
	ID   int               `json:"id"`
	Data map[string]string `json:"data"`
}

func (d *Record) Copy() Record {
	values := d.Data
	newMap := map[string]string{}
	for key, value := range values {
		newMap[key] = value
	}
	return Record{
		ID:   d.ID,
		Data: newMap,
	}
}

type VersionedRecord struct {
	ID        int               `json:"id"`
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	Data      map[string]string `json:"data"`
}

type VersionMeta struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}
