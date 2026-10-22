package postgres

import (
	"reflect"
	"sync"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestProducerStoreFinalMeasurementsStateAndCheckpointAreAtomic(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 250)
	f.producerRaw(11, "v", "g", 120, 1250)
	f.processProducer(run.Model.Name)
	for id := int64(12); id <= 14; id++ {
		f.capture(id)
		f.processProducer(run.Model.Name)
	}
	_, before := f.producerRecovery(run.Model.Name)
	// Closure inserts the complete passage first. Reject its following incomplete
	// entry to verify that the earlier result and reception state also roll back.
	f.exec(`ALTER TABLE vialis.bottlenecks_mediciones ADD CONSTRAINT reject_incomplete CHECK (completa)`)
	f.capture(15)
	if _, err = f.store.ProcessNext(f.ctx, run.Model.Name); err == nil {
		t.Fatal("finalization SQL failure accepted")
	}
	recovered, after := f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 14 || !reflect.DeepEqual(before, after) || f.count("bottlenecks_mediciones") != 0 {
		t.Fatal("partial closure escaped rollback")
	}
	f.exec(`ALTER TABLE vialis.bottlenecks_mediciones DROP CONSTRAINT reject_incomplete`)

	type outcome struct {
		progress bottlenecks.ProducerProgress
		err      error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.store.ProcessNext(f.ctx, run.Model.Name)
			results <- outcome{p, err}
		}()
	}
	wg.Wait()
	close(results)
	processed, passages, incomplete := 0, 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.progress.Processed {
			processed++
		}
		passages += result.progress.Passages
		incomplete += result.progress.Incomplete
	}
	recovered, after = f.producerRecovery(run.Model.Name)
	if processed != 1 || passages != 1 || incomplete != 1 || f.count("bottlenecks_mediciones") != 2 || recovered.Checkpoint != 15 || len(after) != 1 || after[0].Open != nil {
		t.Fatalf("concurrent closure duplicated or lost evidence: processed=%d passages=%d incomplete=%d run=%+v states=%+v", processed, passages, incomplete, recovered, after)
	}
	if retry := f.processProducer(run.Model.Name); retry.Processed || f.count("bottlenecks_mediciones") != 2 {
		t.Fatal("retry duplicated final evidence")
	}
}
