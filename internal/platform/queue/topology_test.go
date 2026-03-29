package queue

import "testing"

func TestResolveQueueTopologyDefaults(t *testing.T) {
	topology := ResolveQueueTopology("releasea.worker")

	if !topology.DeadLetterEnabled {
		t.Fatalf("expected DLQ enabled by default")
	}
	if topology.DeadLetterQueueName != "releasea.worker.dead-letter" {
		t.Fatalf("unexpected default DLQ name: %q", topology.DeadLetterQueueName)
	}
}

func TestResolveQueueTopologyCanDisableDLQ(t *testing.T) {
	t.Setenv("WORKER_QUEUE_DLQ_ENABLE", "false")

	topology := ResolveQueueTopology("releasea.worker")
	if topology.DeadLetterEnabled {
		t.Fatalf("expected DLQ disabled")
	}
	if topology.DeadLetterQueueName != "" {
		t.Fatalf("expected empty DLQ name, got %q", topology.DeadLetterQueueName)
	}
}
