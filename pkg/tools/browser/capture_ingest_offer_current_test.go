package browser

import (
	"context"
	"testing"
)

func TestCaptureIngestResponseIdentityTracksOriginalOffer(t *testing.T) {
	cs, _ := adapterFixture(t)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	epoch := adapterBind(t, cs, parent)
	accept := func(id, gen uint64, target string) {
		t.Helper()
		if _, err := cs.HandleIngestOfferForBinding(context.Background(), epoch, id, "sdp", gen, target); err != nil {
			t.Fatal(err)
		}
	}
	accept(9, 1, "page-a")
	if !cs.IsCurrentIngestOffer(epoch, 9, 1, "page-a") {
		t.Fatal("accepted original offer is not publishable")
	}
	for _, invalid := range []struct {
		epoch, id, gen uint64
		target         string
	}{
		{0, 9, 1, "page-a"}, {epoch, 0, 1, "page-a"}, {epoch, 10, 1, "page-a"}, {epoch, 9, 2, "page-a"}, {epoch, 9, 1, "other"},
	} {
		if cs.IsCurrentIngestOffer(invalid.epoch, invalid.id, invalid.gen, invalid.target) {
			t.Errorf("unaccepted response identity published: %+v", invalid)
		}
	}
	accept(10, 1, "page-a")
	if cs.IsCurrentIngestOffer(epoch, 9, 1, "page-a") || !cs.IsCurrentIngestOffer(epoch, 10, 1, "page-a") {
		t.Fatal("new offer did not retire only the old response")
	}
	next, err := cs.BeginFrameTransition("page-b", 640, 480, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cs.IsCurrentIngestOffer(epoch, 10, 1, "page-a") {
		t.Fatal("changed frame retained an old answer")
	}
	accept(11, next.Generation, next.TargetID)
	if !cs.IsCurrentIngestOffer(epoch, 11, next.Generation, next.TargetID) {
		t.Fatal("new frame's accepted offer is not publishable")
	}
	cancel()
	if cs.IsCurrentIngestOffer(epoch, 11, next.Generation, next.TargetID) {
		t.Fatal("canceled original socket retained an answer")
	}
}

func TestCaptureIngestResponseIdentityRejectsRebindingAndStop(t *testing.T) {
	cs, _ := adapterFixture(t)
	old := adapterBind(t, cs, context.Background())
	if _, err := cs.HandleIngestOfferForBinding(context.Background(), old, 9, "sdp", 1, "page-a"); err != nil {
		t.Fatal(err)
	}
	next := adapterBind(t, cs, context.Background())
	if cs.IsCurrentIngestOffer(old, 9, 1, "page-a") || cs.IsCurrentIngestOffer(next, 9, 1, "page-a") {
		t.Fatal("binding replacement inherited an old answer")
	}
	if _, err := cs.HandleIngestOfferForBinding(context.Background(), next, 1, "sdp", 1, "page-a"); err != nil {
		t.Fatal(err)
	}
	if !cs.IsCurrentIngestOffer(next, 1, 1, "page-a") {
		t.Fatal("replacement offer is not publishable")
	}
	cs.Stop()
	if cs.IsCurrentIngestOffer(next, 1, 1, "page-a") {
		t.Fatal("stopped capture retained a publishable answer")
	}
}
