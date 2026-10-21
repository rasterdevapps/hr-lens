package api

import "testing"

func TestEmailReportValidate(t *testing.T) {
	day := 1
	valid := func() emailReportInput {
		return emailReportInput{Name: "Daily summary", Report: "daily_summary", Frequency: "weekly", SendTime: "09:00",
			Weekday: &day, Recipients: []string{" Boss@Example.com ", "boss@example.com", ""}}
	}
	in := valid()
	if errs := in.validate(); len(errs) != 0 {
		t.Fatalf("valid input rejected: %v", errs)
	}
	if len(in.Recipients) != 1 || in.Recipients[0] != "boss@example.com" {
		t.Errorf("recipients not tidied: %v", in.Recipients)
	}

	cases := []struct {
		field  string
		change func(*emailReportInput)
	}{
		{"name", func(in *emailReportInput) { in.Name = " " }},
		{"report", func(in *emailReportInput) { in.Report = "everything" }},
		{"frequency", func(in *emailReportInput) { in.Frequency = "hourly" }},
		{"sendTime", func(in *emailReportInput) { in.SendTime = "24:00" }},
		{"weekday", func(in *emailReportInput) { in.Weekday = nil }},
		{"monthDay", func(in *emailReportInput) { in.Frequency = "monthly"; bad := 31; in.MonthDay = &bad }},
		{"recipients", func(in *emailReportInput) { in.Recipients = nil }},
		{"recipients", func(in *emailReportInput) { in.Recipients = []string{"boss@example"} }},
		{"timeZone", func(in *emailReportInput) { in.TimeZone = "Mars/Base" }},
	}
	for _, c := range cases {
		in := valid()
		c.change(&in)
		if errs := in.validate(); errs[c.field] == "" {
			t.Errorf("expected an error for %s, got %v", c.field, errs)
		}
	}
}
