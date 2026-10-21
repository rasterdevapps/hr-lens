package api

import "testing"

func TestUserInputValidate(t *testing.T) {
	valid := func() userInput {
		return userInput{EmployeeCode: "EMP-001", FirstName: "Mary-Ann", LastName: "O'Neil",
			Email: "mary@example.com", Mobile: "9876543210", TimeZone: "Asia/Kolkata"}
	}
	if in := valid(); len(in.validate()) != 0 {
		t.Fatalf("valid input rejected: %v", in.validate())
	}

	cases := []struct {
		field  string
		change func(*userInput)
	}{
		{"firstName", func(in *userInput) { in.FirstName = "John3" }},
		{"lastName", func(in *userInput) { in.LastName = "  " }},
		{"employeeCode", func(in *userInput) { in.EmployeeCode = "EMP 001" }},
		{"email", func(in *userInput) { in.Email = "mary@example" }},
		{"email", func(in *userInput) { in.Email = "Mary <mary@example.com>" }},
		{"mobile", func(in *userInput) { in.Mobile = "12345" }},
		{"mobile", func(in *userInput) { in.Mobile = "98a7654321" }},
		{"mobile", func(in *userInput) { in.Mobile = "+91 9876543210" }},
		{"mobile", func(in *userInput) { in.Mobile = "1234567890123456" }},
		{"timeZone", func(in *userInput) { in.TimeZone = "Mars/Base" }},
	}
	for _, c := range cases {
		in := valid()
		c.change(&in)
		if errs := in.validate(); errs[c.field] == "" {
			t.Errorf("%+v: expected an error for %s, got %v", in, c.field, errs)
		}
	}

	in := valid()
	in.Mobile = ""
	in.FirstName = "Śrī"
	if errs := in.validate(); len(errs) != 0 {
		t.Errorf("empty mobile or accented name rejected: %v", errs)
	}
}
