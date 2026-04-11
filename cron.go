package ramune

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cronEntry struct {
	name     string
	schedule cronSchedule
	next     time.Time
}

type cronManager struct {
	mu      sync.Mutex
	entries []cronEntry
	fired   []string // names of entries that fired, consumed by ProcessEvents
	wake    func()
}

func (cm *cronManager) ProcessEvents(r *Runtime) {
	cm.mu.Lock()
	if len(cm.entries) == 0 {
		cm.mu.Unlock()
		return
	}
	now := time.Now()
	for i := range cm.entries {
		if !cm.entries[i].next.IsZero() && now.After(cm.entries[i].next) {
			cm.fired = append(cm.fired, cm.entries[i].name)
			cm.entries[i].next = cm.entries[i].schedule.nextAfter(now)
		}
	}
	fired := cm.fired
	cm.fired = nil
	cm.mu.Unlock()

	for _, name := range fired {
		nameJSON, _ := json.Marshal(name)
		r.execLocked(`if (globalThis.__cronFire) globalThis.__cronFire(` + string(nameJSON) + `);`)
	}
}

func (cm *cronManager) HasActive() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return len(cm.entries) > 0
}

func (cm *cronManager) Close() {
	cm.mu.Lock()
	cm.entries = nil
	cm.mu.Unlock()
}

func (cm *cronManager) add(name, schedule string) error {
	s, err := parseCron(schedule)
	if err != nil {
		return err
	}
	cm.mu.Lock()
	cm.entries = append(cm.entries, cronEntry{
		name:     name,
		schedule: s,
		next:     s.nextAfter(time.Now()),
	})
	cm.mu.Unlock()
	return nil
}

// cronSchedule represents a parsed cron expression (minute, hour, day, month, weekday).
type cronSchedule struct {
	minute  []int // 0-59
	hour    []int // 0-23
	day     []int // 1-31
	month   []int // 1-12
	weekday []int // 0-6 (Sunday=0)
}

func (s cronSchedule) nextAfter(t time.Time) time.Time {
	t = t.Add(time.Minute).Truncate(time.Minute)
	for i := 0; i < 525600; i++ { // scan up to 1 year
		if s.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{} // should not happen
}

func (s cronSchedule) matches(t time.Time) bool {
	return cronFieldMatches(s.minute, t.Minute()) &&
		cronFieldMatches(s.hour, t.Hour()) &&
		cronFieldMatches(s.day, t.Day()) &&
		cronFieldMatches(s.month, int(t.Month())) &&
		cronFieldMatches(s.weekday, int(t.Weekday()))
}

func cronFieldMatches(set []int, val int) bool {
	if len(set) == 0 {
		return true // wildcard
	}
	for _, v := range set {
		if v == val {
			return true
		}
	}
	return false
}

func parseCron(expr string) (cronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronSchedule{}, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}
	minute, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("cron minute: %w", err)
	}
	hour, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("cron hour: %w", err)
	}
	day, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("cron day: %w", err)
	}
	month, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("cron month: %w", err)
	}
	weekday, err := parseCronField(fields[4], 0, 6)
	if err != nil {
		return cronSchedule{}, fmt.Errorf("cron weekday: %w", err)
	}
	return cronSchedule{minute: minute, hour: hour, day: day, month: month, weekday: weekday}, nil
}

func parseCronField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return nil, nil // wildcard
	}
	var result []int
	for _, part := range strings.Split(field, ",") {
		if strings.Contains(part, "/") {
			// step: */N or M-N/S
			parts := strings.SplitN(part, "/", 2)
			step, err := strconv.Atoi(parts[1])
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step: %s", part)
			}
			start, end := min, max
			if parts[0] != "*" {
				rng := strings.SplitN(parts[0], "-", 2)
				start, err = strconv.Atoi(rng[0])
				if err != nil {
					return nil, fmt.Errorf("invalid range: %s", part)
				}
				if len(rng) == 2 {
					end, err = strconv.Atoi(rng[1])
					if err != nil {
						return nil, fmt.Errorf("invalid range: %s", part)
					}
				}
			}
			for i := start; i <= end; i += step {
				result = append(result, i)
			}
		} else if strings.Contains(part, "-") {
			rng := strings.SplitN(part, "-", 2)
			lo, err1 := strconv.Atoi(rng[0])
			hi, err2 := strconv.Atoi(rng[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid range: %s", part)
			}
			for i := lo; i <= hi; i++ {
				result = append(result, i)
			}
		} else {
			v, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid value: %s", part)
			}
			result = append(result, v)
		}
	}
	return result, nil
}

func goBunCronAdd(cm *cronManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("cron: name and schedule required")
		}
		name, _ := args[0].(string)
		schedule, _ := args[1].(string)
		if err := cm.add(name, schedule); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func goBunCronRemove(cm *cronManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("cron: name required")
		}
		name, _ := args[0].(string)
		cm.mu.Lock()
		for i := 0; i < len(cm.entries); i++ {
			if cm.entries[i].name == name {
				cm.entries = append(cm.entries[:i], cm.entries[i+1:]...)
				break
			}
		}
		cm.mu.Unlock()
		return nil, nil
	}
}

func goBunCronList(cm *cronManager) func([]any) (any, error) {
	return func(args []any) (any, error) {
		cm.mu.Lock()
		names := make([]string, 0, len(cm.entries))
		for _, e := range cm.entries {
			names = append(names, e.name)
		}
		cm.mu.Unlock()
		out, _ := json.Marshal(names)
		return string(out), nil
	}
}

func (r *Runtime) installCron() error {
	cm := &cronManager{wake: r.Wake}
	r.customTickMgrs = append(r.customTickMgrs, cm)

	if err := r.registerFuncLocked("__go_cron_add", goBunCronAdd(cm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_cron_remove", goBunCronRemove(cm)); err != nil {
		return err
	}
	if err := r.registerFuncLocked("__go_cron_list", goBunCronList(cm)); err != nil {
		return err
	}

	return r.execLocked(`(function() {
	var _handlers = {};
	globalThis.__cronFire = function(name) {
		if (_handlers[name]) _handlers[name]();
	};
	globalThis.Ramune.cron = function(name, schedule, handler) {
		_handlers[name] = handler;
		__go_cron_add(name, schedule);
	};
	globalThis.Ramune.cron.remove = function(name) {
		delete _handlers[name];
		__go_cron_remove(name);
	};
	globalThis.Ramune.cron.list = function() {
		return JSON.parse(__go_cron_list());
	};
})();`)
}
