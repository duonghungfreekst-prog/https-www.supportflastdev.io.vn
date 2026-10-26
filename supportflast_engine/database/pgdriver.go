package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

func init() {
	sql.Register("pgcompat", &pgCompatDriver{parent: &pq.Driver{}})
}

var (
	reBoolEqZero    = regexp.MustCompile(`(?i)\b((?:[a-zA-Z0-9_]+\.)?(?:is_deleted|is_dir|is_encrypted|has_missing_chunks))\s*=\s*0\b`)
	reBoolEqOne     = regexp.MustCompile(`(?i)\b((?:[a-zA-Z0-9_]+\.)?(?:is_deleted|is_dir|is_encrypted|has_missing_chunks))\s*=\s*1\b`)
	reBoolNeqZero   = regexp.MustCompile(`(?i)\b((?:[a-zA-Z0-9_]+\.)?(?:is_deleted|is_dir|is_encrypted|has_missing_chunks))\s*(!=|<>)\s*0\b`)
	reBoolNeqOne    = regexp.MustCompile(`(?i)\b((?:[a-zA-Z0-9_]+\.)?(?:is_deleted|is_dir|is_encrypted|has_missing_chunks))\s*(!=|<>)\s*1\b`)
	reZeroEqBool    = regexp.MustCompile(`(?i)\b0\s*=\s*((?:[a-zA-Z0-9_]+\.)?(?:is_deleted|is_dir|is_encrypted|has_missing_chunks))\b`)
	reOneEqBool     = regexp.MustCompile(`(?i)\b1\s*=\s*((?:[a-zA-Z0-9_]+\.)?(?:is_deleted|is_dir|is_encrypted|has_missing_chunks))\b`)
	reCoalesceIsDir = regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-zA-Z0-9_]+\.)?is_dir)\s*,\s*0\s*\)`)
)

// RebindPostgres chuyển đổi câu lệnh SQL tương thích PostgreSQL:
// 1. Chuyển placeholder '?' thành '$1', '$2', '$3'... (bỏ qua '?' trong chuỗi string literal và comment)
// 2. Chuyển dấu backtick `column` thành "column"
// 3. Chuyển INSERT IGNORE INTO thành INSERT INTO ... ON CONFLICT DO NOTHING
// 4. Chuyển REPLACE INTO cho các bảng hỗ trợ UPSERT
func RebindPostgres(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return q
	}

	// Xử lý kiểu BOOLEAN cho PostgreSQL (virtual_files: is_deleted, is_dir, is_encrypted, has_missing_chunks)
	lowerQ := strings.ToLower(q)
	if strings.Contains(lowerQ, "is_deleted") ||
		strings.Contains(lowerQ, "is_dir") ||
		strings.Contains(lowerQ, "is_encrypted") ||
		strings.Contains(lowerQ, "has_missing_chunks") {
		q = reBoolEqZero.ReplaceAllString(q, "${1} = FALSE")
		q = reBoolEqOne.ReplaceAllString(q, "${1} = TRUE")
		q = reBoolNeqZero.ReplaceAllString(q, "${1} = TRUE")
		q = reBoolNeqOne.ReplaceAllString(q, "${1} = FALSE")
		q = reZeroEqBool.ReplaceAllString(q, "FALSE = ${1}")
		q = reOneEqBool.ReplaceAllString(q, "TRUE = ${1}")
		q = reCoalesceIsDir.ReplaceAllString(q, "CASE WHEN ${1} = TRUE THEN 1 ELSE 0 END")
	}

	// Xử lý INSERT IGNORE INTO hoặc INSERT OR IGNORE INTO
	upperQ := strings.ToUpper(q)
	if strings.HasPrefix(upperQ, "INSERT IGNORE INTO") {
		q = "INSERT INTO" + q[len("INSERT IGNORE INTO"):]
		if !strings.Contains(strings.ToUpper(q), "ON CONFLICT") {
			q = strings.TrimSuffix(strings.TrimSpace(q), ";") + " ON CONFLICT DO NOTHING"
		}
	} else if strings.HasPrefix(upperQ, "INSERT OR IGNORE INTO") {
		q = "INSERT INTO" + q[len("INSERT OR IGNORE INTO"):]
		if !strings.Contains(strings.ToUpper(q), "ON CONFLICT") {
			q = strings.TrimSuffix(strings.TrimSpace(q), ";") + " ON CONFLICT DO NOTHING"
		}
	}

	var buf strings.Builder
	buf.Grow(len(q) + 32)

	inString := false
	inLineComment := false
	inBlockComment := false
	paramIndex := 1
	n := len(q)

	for i := 0; i < n; i++ {
		c := q[i]

		// Trong chuỗi string literal
		if inString {
			buf.WriteByte(c)
			if c == '\'' {
				if i+1 < n && q[i+1] == '\'' {
					buf.WriteByte('\'')
					i++
				} else {
					inString = false
				}
			}
			continue
		}

		// Trong ghi chú dòng đơn
		if inLineComment {
			buf.WriteByte(c)
			if c == '\n' {
				inLineComment = false
			}
			continue
		}

		// Trong ghi chú khối
		if inBlockComment {
			buf.WriteByte(c)
			if c == '*' && i+1 < n && q[i+1] == '/' {
				buf.WriteByte('/')
				i++
				inBlockComment = false
			}
			continue
		}

		// Bắt đầu ghi chú dòng đơn
		if c == '-' && i+1 < n && q[i+1] == '-' {
			inLineComment = true
			buf.WriteString("--")
			i++
			continue
		}

		// Bắt đầu ghi chú khối
		if c == '/' && i+1 < n && q[i+1] == '*' {
			inBlockComment = true
			buf.WriteString("/*")
			i++
			continue
		}

		// Bắt đầu chuỗi string
		if c == '\'' {
			inString = true
			buf.WriteByte('\'')
			continue
		}

		// Đổi backtick ` thành "
		if c == '`' {
			buf.WriteByte('"')
			continue
		}

		// Đổi ? thành $1, $2, ...
		if c == '?' {
			buf.WriteString(fmt.Sprintf("$%d", paramIndex))
			paramIndex++
			continue
		}

		buf.WriteByte(c)
	}

	return buf.String()
}

type pgCompatDriver struct {
	parent *pq.Driver
}

func (d *pgCompatDriver) Open(name string) (driver.Conn, error) {
	c, err := d.parent.Open(name)
	if err != nil {
		return nil, err
	}
	return &pgCompatConn{parent: c}, nil
}

type pgCompatConn struct {
	parent driver.Conn
}

func (c *pgCompatConn) Prepare(query string) (driver.Stmt, error) {
	return c.parent.Prepare(RebindPostgres(query))
}

func (c *pgCompatConn) Close() error {
	return c.parent.Close()
}

func (c *pgCompatConn) Begin() (driver.Tx, error) {
	return c.parent.Begin()
}

func (c *pgCompatConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.parent.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, RebindPostgres(query))
	}
	return c.parent.Prepare(RebindPostgres(query))
}

func (c *pgCompatConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if ex, ok := c.parent.(driver.Execer); ok {
		return ex.Exec(RebindPostgres(query), args)
	}
	stmt, err := c.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.Exec(args)
}

func (c *pgCompatConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if ec, ok := c.parent.(driver.ExecerContext); ok {
		return ec.ExecContext(ctx, RebindPostgres(query), args)
	}
	dargs := make([]driver.Value, len(args))
	for i, arg := range args {
		dargs[i] = arg.Value
	}
	return c.Exec(query, dargs)
}

func (c *pgCompatConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	if q, ok := c.parent.(driver.Queryer); ok {
		return q.Query(RebindPostgres(query), args)
	}
	stmt, err := c.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	return stmt.Query(args)
}

func (c *pgCompatConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if qc, ok := c.parent.(driver.QueryerContext); ok {
		return qc.QueryContext(ctx, RebindPostgres(query), args)
	}
	dargs := make([]driver.Value, len(args))
	for i, arg := range args {
		dargs[i] = arg.Value
	}
	return c.Query(query, dargs)
}

func (c *pgCompatConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if btx, ok := c.parent.(driver.ConnBeginTx); ok {
		return btx.BeginTx(ctx, opts)
	}
	return c.parent.Begin()
}

func (c *pgCompatConn) Ping(ctx context.Context) error {
	if p, ok := c.parent.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *pgCompatConn) ResetSession(ctx context.Context) error {
	if rs, ok := c.parent.(driver.SessionResetter); ok {
		return rs.ResetSession(ctx)
	}
	return nil
}

func (c *pgCompatConn) CheckNamedValue(nv *driver.NamedValue) error {
	if cnv, ok := c.parent.(driver.NamedValueChecker); ok {
		return cnv.CheckNamedValue(nv)
	}
	return nil
}
