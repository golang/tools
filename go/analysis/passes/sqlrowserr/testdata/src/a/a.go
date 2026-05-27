package a

import "database/sql"

//
// (Ballast comment to make it easier to adjust line numbers when imports change.)
//

func missingErr(db *sql.DB) {
	rows, err := db.Query("") // want `sql.Rows "rows" is used in Next loop at line 15 without final check of rows.Err\(\)`
	if err != nil {
		return
	}
	defer rows.Close() // ignore error
	for rows.Next() {  // L15
		println(rows.Scan())
	}
}

func missingErr2(db *sql.DB) {
	rows, _ := db.QueryContext(nil, "") // want `sql.Rows "rows" is used in Next loop at line 23 without final check of rows.Err\(\)`
	for {
		if !rows.Next() { // L23
			break
		}
		println(rows.Scan())
	}
}

func stmt(stmt *sql.Stmt) {
	{
		rows, _ := stmt.QueryContext(nil, "") // want `sql.Rows "rows" is used in Next loop at line 33 without final check of rows.Err\(\)`
		for rows.Next() {                     // L33
			rows.Scan()
		}
	}
	{
		rows, _ := stmt.Query("") // want `sql.Rows "rows" is used in Next loop at line .. without final check of rows.Err\(\)`
		for rows.Next() {
			rows.Scan()
		}
	}
}

func tx(tx *sql.Tx) {
	{
		rows, _ := tx.QueryContext(nil, "") // want `sql.Rows "rows" is used in Next loop at line .. without final check of rows.Err\(\)`
		for rows.Next() {
			rows.Scan()
		}
	}
	{
		rows, _ := tx.Query("") // want `sql.Rows "rows" is used in Next loop at line .. without final check of rows.Err\(\)`
		for rows.Next() {
			rows.Scan()
		}
	}
}

func conn(conn *sql.Conn) {
	rows, _ := conn.QueryContext(nil, "") // want `sql.Rows "rows" is used in Next loop at line .. without final check of rows.Err\(\)`
	for rows.Next() {
		rows.Scan()
	}
}

func nopeErrIsChecked(db *sql.DB) {
	rows, _ := db.Query("")
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
}

func nopeErrIsCalled(db *sql.DB) {
	rows, _ := db.Query("")
	for rows.Next() {
		println(rows.Scan())
	}
	_ = rows.Err() // ignore error
}

func nopeRowsIsParam(rows *sql.Rows) {
	for rows.Next() {
	}
}

func nopeRowsEscapes(rows *sql.Rows) {
	for rows.Next() {
	}
	arbitraryEffects(rows)
}

func arbitraryEffects(any)
