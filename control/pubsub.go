package control

import (
	"fmt"

	"github.com/cskr/pubsub"
)

var (
	bus *pubsub.PubSub
)

const (
	TopicGlobal  = "cmd"
	TopicSyncAll = "sync"
	TopicSync_   = "sync-"
	TopicState   = "state"
)

type CommandMessage int

const (
	MessageHalt = iota
	MessageRestart
	MessagePause
	MessageResume
	MessageEnable
	MessageDisable
	MessageSyncLoop
	MessageResync
	MessageResyncDry
	MessagePublishState
)

func init() {
	bus = pubsub.New(0)
}

func GetBus() *pubsub.PubSub {
	return bus
}

func MessageFromString(text string) (int, error) {

	switch text {
	case "exit", "quit":
		// Stop all
		return MessageHalt, nil
	case "resync":
		// Full resync
		return MessageResync, nil
	case "dry":
		// Full resync with dry run
		return MessageResyncDry, nil
	case "loop":
		// Check Snapshot
		// Use dryRun as Force Resync
		return MessageSyncLoop, nil
	case "enable":
		// Enable one sync
		return MessageEnable, nil
	case "disable":
		// Stop and disable one sync
		return MessageDisable, nil
	case "pause":
		// Pause all syncs
		return MessagePause, nil
	case "resume":
		// Resume all syncs
		return MessageResume, nil
	default:
		return -1, fmt.Errorf("cannot find corresponding command")
	}

}
