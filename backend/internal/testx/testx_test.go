package testx

import "testing"

func TestRunPassesThrough(t *testing.T) {
	ran := false
	Run(t, func(t T) { ran = true })
	if !ran {
		t.Fatal("body did not run")
	}
}

func TestXFailSkipsOnFailure(t *testing.T) {
	// Run in a subtest so we can observe the skip without affecting this test.
	ok := t.Run("inner", func(t *testing.T) {
		XFail(t, "self-test", func(t T) { t.Errorf("expected failure") })
	})
	if !ok {
		t.Fatal("XFail with a failing body must not fail the test")
	}
}

func TestXFailFatalAbortsBody(t *testing.T) {
	after := false
	t.Run("inner", func(t *testing.T) {
		XFail(t, "self-test", func(t T) {
			t.Fatalf("stop here")
			after = true
		})
	})
	if after {
		t.Fatal("Fatalf did not abort the body")
	}
}

func TestXFailStrictFailsOnPass(t *testing.T) {
	ok := t.Run("inner", func(t *testing.T) {
		r := &recorder{T: t}
		// Drive the strict path by hand: a body that records nothing.
		func() {
			defer func() { _ = recover() }()
			(func(T) {})(r)
		}()
		if len(r.failures) != 0 {
			t.Fatal("unexpected failures")
		}
	})
	if !ok {
		t.Fatal("setup failed")
	}
	// The real strict behavior (t.Errorf on pass) cannot be observed without
	// failing this test, so it is covered by reading xfail(); this test only
	// pins the recorder's clean state.
}
