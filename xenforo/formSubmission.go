package xenforo

// FormSubmission models the Snog Forms submission table, xf_snog_forms_logs,
// which holds one row per form a member submits. It maps only the columns
// the LOA route reads. Nothing in it names the thread the submission posted.
type FormSubmission struct {
	LogID   uint32 `gorm:"column:log_id;primaryKey"`
	FormID  uint32 `gorm:"column:form_id"`
	LogDate uint32 `gorm:"column:log_date"`
}

func (FormSubmission) TableName() string { return "xf_snog_forms_logs" }

// FormAnswer models the Snog Forms answer table, xf_snog_forms_answers,
// which holds a submission's answer to each question. Answers are BBCode
// text. It maps only the columns the LOA route reads.
type FormAnswer struct {
	AnswerID   uint32 `gorm:"column:answer_id;primaryKey"`
	LogID      uint32 `gorm:"column:log_id"`
	QuestionID uint32 `gorm:"column:questionid"`
	Answer     string `gorm:"column:answer"`
}

func (FormAnswer) TableName() string { return "xf_snog_forms_answers" }
