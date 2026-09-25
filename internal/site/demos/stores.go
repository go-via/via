package demos

import "github.com/go-via/via/topic"

// No card shows this file. It hands tests the stores the counter demos share:
// a _test.go in this package would be embedded and served, so those tests
// live in demos/storetest and need exported names to reach the stores.

// StartStore is the Getting started counter's topic and its locked read.
func StartStore() (*topic.Topic[int64], func() int64) { return startMoved, startLoad }

// SharedStore is the /live shared counter's topic and its locked read.
func SharedStore() (*topic.Topic[int64], func() int64) { return sharedTopic, sharedLoad }

// LandingCount is the front page counter's current value.
func LandingCount() int64 { return landingCount.Load() }

// ChatPresenceStore is the tutorial chat's head-count topic, its locked read,
// and the step a connect or disconnect applies.
func ChatPresenceStore() (*topic.Topic[int64], func() int64, func(int64)) {
	return tutorialRoom.presence, tutorialRoom.count, tutorialRoom.add
}

// PresenceStore is the /live presence count's topic, its locked read, and the
// step a connect or disconnect applies.
func PresenceStore() (*topic.Topic[int64], func() int64, func(int64)) {
	return presenceTopic, presenceLoad, presenceAdd
}
