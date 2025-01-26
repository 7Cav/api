package xenforo

type TableInfo struct {
	TableName  string
	UpdateTime string
}

func (TableInfo) SchemaTableName() string {
	return "information_schema.tables"
}
