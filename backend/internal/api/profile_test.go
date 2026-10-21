package api

import "testing"

func TestProfileDetailsValidate(t *testing.T) {
	var d profileDetails
	if errs := d.validate(); len(errs) != 0 {
		t.Fatalf("empty details rejected: %v", errs)
	}
	d.Primary.Gender = "x"
	d.Primary.DateOfBirth = "2999-01-01"
	d.Contact.WorkMail = "nope"
	d.Contact.MobilePhone = "12"
	n := 13
	d.Job.NoticeMonths = &n
	d.Relations = []profileRelation{{Name: " ", Relation: "", Phone: "abc"}}
	errs := d.validate()
	for _, k := range []string{"primary.gender", "primary.dateOfBirth", "contact.workMail", "contact.mobilePhone", "job.noticeMonths",
		"relations.0.name", "relations.0.relation", "relations.0.phone"} {
		if errs[k] == "" {
			t.Errorf("expected an error for %s, got %v", k, errs)
		}
	}

	clean, errs := validateDocument("degree", map[string]any{"degree": "B.E", "branch": "CSE", "yearOfJoining": "2012-06-01",
		"yearOfCompletion": "2008-05-01", "cgpa": "8", "university": "Anna"})
	if errs["yearOfCompletion"] == "" || clean["degree"] != "B.E" {
		t.Errorf("completion before joining accepted: %v %v", clean, errs)
	}
	if _, errs := validateDocument("identity", map[string]any{}); errs["documentType"] == "" || errs["number"] == "" {
		t.Errorf("missing identity fields accepted: %v", errs)
	}
}
