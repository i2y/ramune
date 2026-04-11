package ramune_test

import (
	"testing"
)

func TestCronAPI(t *testing.T) {
	r := sharedNodeCompat(t)

	v, err := r.Eval(`
		var registered = false;
		Ramune.cron('test-job', '*/5 * * * *', function() { registered = true; });
		var list = Ramune.cron.list();
		Ramune.cron.remove('test-job');
		var afterRemove = Ramune.cron.list();
		JSON.stringify({ list: list, afterRemove: afterRemove });
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `{"list":["test-job"],"afterRemove":[]}` {
		t.Fatalf("got %s", s)
	}
}

func TestCronParseSchedule(t *testing.T) {
	r := sharedNodeCompat(t)

	// Register with various cron expressions to test parser
	v, err := r.Eval(`
		Ramune.cron('every-min', '* * * * *', function() {});
		Ramune.cron('specific', '30 9 * * 1-5', function() {});
		Ramune.cron('step', '*/10 * * * *', function() {});
		var list = Ramune.cron.list();
		Ramune.cron.remove('every-min');
		Ramune.cron.remove('specific');
		Ramune.cron.remove('step');
		JSON.stringify(list);
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s, _ := v.GoString()
	if s != `["every-min","specific","step"]` {
		t.Fatalf("got %s", s)
	}
}
