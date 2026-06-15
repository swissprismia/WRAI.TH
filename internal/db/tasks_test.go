package db

import "testing"

// TestCountActiveTasks covers the ADF-025 scale signal: pending + in-progress
// tasks, optionally role-scoped, excluding terminal states and archived rows.
func TestCountActiveTasks(t *testing.T) {
	d := testDB(t)

	// Seed tasks across projects, roles, and statuses. DispatchTask creates
	// them pending; StartTask/CompleteTask move them along.
	dispatch := func(project, slug string) string {
		task, err := d.DispatchTask(project, slug, "user", "t-"+slug, "", "", nil, nil, nil)
		if err != nil {
			t.Fatalf("dispatch %s: %v", slug, err)
		}
		return task.ID
	}

	dispatch("app-demos", "app-demos-backend")            // pending, worker role
	inProg := dispatch("app-demos", "app-demos-frontend") // -> in-progress, worker role
	dispatch("app-demos", "app-demos-cto")                // pending, executive role
	dispatch("agt-bothawui", "agt-bothawui-researcher")   // pending, worker role
	dispatch("transversal", "architect-transversal")      // pending, prefix-matched role
	done := dispatch("app-demos", "app-demos-qa")         // -> done, must be excluded

	if _, err := d.StartTask(inProg, "adf-worker-1", "app-demos"); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := d.CompleteTask(done, "adf-worker-1", "app-demos", nil); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	cases := []struct {
		name  string
		roles []string
		want  int
	}{
		// No role filter: every active (pending + in-progress) task, done excluded.
		{"all active", nil, 5},
		// Execution-worker roles: backend, frontend (in-progress), researcher,
		// architect (prefix). cto excluded; done qa excluded.
		{"worker roles", []string{"backend", "frontend", "qa", "researcher", "architect", "engineer"}, 4},
		// Executive pool sees only its own work.
		{"cto only", []string{"cto", "director"}, 1},
		// A role nobody is dispatched to yields zero.
		{"unused role", []string{"devops"}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.CountActiveTasks(tc.roles)
			if err != nil {
				t.Fatalf("CountActiveTasks(%v): %v", tc.roles, err)
			}
			if got != tc.want {
				t.Errorf("CountActiveTasks(%v) = %d, want %d", tc.roles, got, tc.want)
			}
		})
	}
}
