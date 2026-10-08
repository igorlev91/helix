package router

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Storage ops
// Object identity is (collection, key, user_id); value is arbitrary JSON.

type storageWriteRequest struct {
	Collection      string          `json:"collection"`
	Key             string          `json:"key"`
	Value           json.RawMessage `json:"value"`
	PermissionRead  *int            `json:"permission_read,omitempty"`  // default 1 (owner)
	PermissionWrite *int            `json:"permission_write,omitempty"` // default 1 (owner)
	Version         string          `json:"version,omitempty"`          // optimistic concurrency check
}

type storageReadRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	UserID     string `json:"user_id,omitempty"` // empty = own objects
}

type storageDeleteRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
}

type storageListRequest struct {
	Collection string `json:"collection"`
	Limit      int    `json:"limit"`
}

// RegisterStorageOps installs storage.write / storage.read / storage.delete / storage.list.
func (r *Router) RegisterStorageOps(db *sql.DB) {
	ctx := context.Background()

	r.Register("storage.write", func(s *session.Session, data json.RawMessage) (any, error) {
		var req storageWriteRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Collection == "" || req.Key == "" {
			return nil, errors.New("collection and key required")
		}
		if len(req.Value) == 0 {
			req.Value = json.RawMessage(`{}`)
		}

		permRead, permWrite := 1, 1
		if req.PermissionRead != nil {
			permRead = *req.PermissionRead
		}
		if req.PermissionWrite != nil {
			permWrite = *req.PermissionWrite
		}

		// If the object already exists, its write permission applies
		var existingWrite int
		var existingVersion string
		err := db.QueryRowContext(ctx,
			`SELECT write, version FROM storage
			  WHERE collection = $1 AND key = $2 AND user_id = $3`,
			req.Collection, req.Key, s.UserID).Scan(&existingWrite, &existingVersion)
		if err == nil {
			if existingWrite < 1 {
				return nil, errors.New("object is write-protected")
			}
			// Optimistic concurrency: caller-supplied version must match
			if req.Version != "" && req.Version != existingVersion {
				return nil, errors.New("version conflict")
			}
		} else if err != sql.ErrNoRows {
			return nil, err
		}

		version := md5Hex(req.Value)
		_, err = db.ExecContext(ctx,
			`INSERT INTO storage (collection, key, user_id, value, version, read, write)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (collection, key, user_id)
			 DO UPDATE SET value = $4, version = $5, read = $6, write = $7, update_time = now()`,
			req.Collection, req.Key, s.UserID, req.Value, version, permRead, permWrite)
		if err != nil {
			return nil, err
		}
		return map[string]any{"collection": req.Collection, "key": req.Key, "version": version}, nil
	})

	r.Register("storage.read", func(s *session.Session, data json.RawMessage) (any, error) {
		var req storageReadRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Collection == "" || req.Key == "" {
			return nil, errors.New("collection and key required")
		}
		owner := s.UserID
		if req.UserID != "" {
			var err error
			owner, err = uuid.Parse(req.UserID)
			if err != nil {
				return nil, errors.New("invalid user_id")
			}
		}

		var value json.RawMessage
		var version string
		var permRead int
		var updateTime string
		err := db.QueryRowContext(ctx,
			`SELECT value, version, read, update_time FROM storage
			  WHERE collection = $1 AND key = $2 AND user_id = $3`,
			req.Collection, req.Key, owner).Scan(&value, &version, &permRead, &updateTime)
		if err == sql.ErrNoRows {
			return nil, errors.New("object not found")
		}
		if err != nil {
			return nil, err
		}
		// Read permission: 2 = public, otherwise only the owner
		if permRead != 2 && owner != s.UserID {
			return nil, errors.New("read not permitted")
		}
		return map[string]any{
			"collection":  req.Collection,
			"key":         req.Key,
			"user_id":     owner.String(),
			"value":       value,
			"version":     version,
			"update_time": updateTime,
		}, nil
	})

	r.Register("storage.delete", func(s *session.Session, data json.RawMessage) (any, error) {
		var req storageDeleteRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Collection == "" || req.Key == "" {
			return nil, errors.New("collection and key required")
		}
		// Only own objects, and only if write-permitted
		res, err := db.ExecContext(ctx,
			`DELETE FROM storage
			  WHERE collection = $1 AND key = $2 AND user_id = $3 AND write >= 1`,
			req.Collection, req.Key, s.UserID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, errors.New("object not found or delete not permitted")
		}
		return map[string]string{"deleted": req.Key}, nil
	})

	// storage.list -> own objects in a collection
	r.Register("storage.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req storageListRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Collection == "" {
			return nil, errors.New("collection required")
		}
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 20
		}
		rows, err := db.QueryContext(ctx,
			`SELECT key, value, version, update_time FROM storage
			  WHERE collection = $1 AND user_id = $2
			  ORDER BY key LIMIT $3`, req.Collection, s.UserID, req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type obj struct {
			Key        string          `json:"key"`
			Value      json.RawMessage `json:"value"`
			Version    string          `json:"version"`
			UpdateTime string          `json:"update_time"`
		}
		out := []obj{}
		for rows.Next() {
			var o obj
			if err := rows.Scan(&o.Key, &o.Value, &o.Version, &o.UpdateTime); err != nil {
				return nil, err
			}
			out = append(out, o)
		}
		return map[string]any{"collection": req.Collection, "objects": out}, nil
	})
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
