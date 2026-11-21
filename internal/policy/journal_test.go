package policy

import "testing"

func TestJournalSettingIsReadAndRulesStillParse(t *testing.T) {
	const in = `{"journal":true,"rules":[{"match":"rm","min_level":"R3"}]}`
	if on, err := Journal([]byte(in)); err != nil || !on {
		t.Errorf("%v %v", on, err)
	}
	if rules, err := Parse([]byte(in)); err != nil || len(rules) != 1 {
		t.Errorf("%v %v", rules, err)
	}
	if on, err := Journal([]byte(`{"rules":[]}`)); err != nil || on {
		t.Errorf("journal is off unless asked for: %v %v", on, err)
	}
	if _, err := Journal([]byte(`{"journal":"yes"}`)); err == nil {
		t.Error("a journal setting that is not true or false must be refused")
	}
}
