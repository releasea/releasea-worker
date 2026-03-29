package queue

import (
	"os"
	"strings"

	workerutils "releaseaworker/internal/platform/utils"

	amqp "github.com/rabbitmq/amqp091-go"
)

type QueueTopology struct {
	QueueName           string
	DeadLetterEnabled   bool
	DeadLetterQueueName string
	MainQueueArgs       amqp.Table
}

func ResolveQueueTopology(queueName string) QueueTopology {
	topology := QueueTopology{
		QueueName:         queueName,
		DeadLetterEnabled: workerutils.EnvBool("WORKER_QUEUE_DLQ_ENABLE", true),
	}
	if !topology.DeadLetterEnabled {
		return topology
	}

	dlqName := strings.TrimSpace(os.Getenv("WORKER_QUEUE_DLQ_NAME"))
	if dlqName == "" {
		dlqName = queueName + ".dead-letter"
	}
	topology.DeadLetterQueueName = dlqName
	topology.MainQueueArgs = amqp.Table{
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": dlqName,
	}
	return topology
}

func DeclareQueueTopology(ch *amqp.Channel, topology QueueTopology) error {
	if topology.DeadLetterEnabled {
		if _, err := ch.QueueDeclare(
			topology.DeadLetterQueueName,
			true,
			false,
			false,
			false,
			nil,
		); err != nil {
			return err
		}
	}

	_, err := ch.QueueDeclare(
		topology.QueueName,
		true,
		false,
		false,
		false,
		topology.MainQueueArgs,
	)
	return err
}
