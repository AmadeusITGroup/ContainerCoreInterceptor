package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/Shopify/sarama/mocks"
	"github.com/fsouza/go-dockerclient"
)

func TestRestNotificationError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(" HTTP status code returned!"))

		dump, err := httputil.DumpRequest(r, true)
		if err != nil {
			t.Errorf("Cannot dump rest request: %s", err)
			return
		}
		t.Logf("Received REST request: %s", dump)
	}))
	defer ts.Close()
	pushed := true
	var err error
	hostname, err := os.Hostname()
	if err != nil {
		t.Errorf("Cannot get hostname")
	}
	timestamp := time.Now()
	name := "registry.com/bbb/ccc:1.0.1"
	var conf Conf
	conf.RestURI = "http://dummy.com"
	conf.Hostname = hostname
	restErr := notifyRest(conf, pushed, name, id[:12], object, timestamp)
	if restErr == nil {
		t.Error("REST server was expected to return an error")
	}
	conf.RestURI = ts.URL
	restErr = notifyRest(conf, pushed, name, id[:12], object, timestamp)
	if restErr == nil {
		t.Error("REST server was expected to return an error")
	}
}

// This function is used inside the mocks unit tests to generate ValueCheckers
func generateRegexpChecker(re string) func([]byte) error {
	return func(val []byte) error {
		matched, err := regexp.MatchString(re, string(val))
		if err != nil {
			return errors.New("Error while trying to match the input message with the expected pattern: " + err.Error())
		}
		if !matched {
			return fmt.Errorf("No match between input value \"%s\" and expected pattern \"%s\"", val, re)
		}
		return nil
	}
}

func TestKafkaNotificationError(t *testing.T) {
	pushed := true
	hostname, _ := os.Hostname()
	kafkaServer := "localhost"
	var conf Conf
	conf.KafkaServer = kafkaServer
	conf.Hostname = hostname
	name := "registry.com/bbb/ccc:1.0.1"
	sp := mocks.NewSyncProducer(t, nil)
	sp.ExpectSendMessageAndFail(sarama.ErrOutOfBrokers)
	conf.Producer = sp

	kafkaErr := notifyKafka(conf, pushed, name, id[:12], object, time.Now())
	if kafkaErr == nil {
		t.Error("Kafka producer was expected to return an error")
	}

	if err := sp.Close(); err != nil {
		t.Error(err)
	}

}

type result struct {
	ChangesCalled bool
	CommitCalled  bool
	PushCalled    bool
	RemoveCalled  bool
}

type mockClient struct {
	Changes    []docker.Change
	ChangesErr error
	CommitErr  error
	PushErr    error
	RemoveErr  error
	Testing    *testing.T
	Res        result
}

func (c *mockClient) ContainerChanges(id string) ([]docker.Change, error) {
	c.Res.ChangesCalled = true
	return c.Changes, c.ChangesErr
}

func (c *mockClient) CommitContainer(opts docker.CommitContainerOptions) (*docker.Image, error) {
	if opts.Container != id || opts.Repository != expectedName || opts.Tag != id[:12] {
		c.Testing.Errorf("CommitContainer called with wrong values: %#v", opts)
	}
	c.Res.CommitCalled = true
	return &docker.Image{ID: imageID}, c.CommitErr
}

func (c *mockClient) PushImage(opts docker.PushImageOptions, auth docker.AuthConfiguration) error {
	if opts.Tag != id[:12] || opts.Name != expectedName {
		c.Testing.Errorf("PushContainer called with wrong values: %#v", opts)
	}
	c.Res.PushCalled = true
	return c.PushErr
}

func (c *mockClient) RemoveImage(name string) error {
	if name != imageID {
		c.Testing.Errorf("RemoveImage called with wrong values: %s", name)
	}
	c.Res.RemoveCalled = true
	return c.RemoveErr
}

var (
	id           = "9da87179f31c6c4ed34e109c858a5eb950eae8da26d945a0b98e8f1ff365b636"
	imageID      = "f40d32d36a330bca9e55a5759d03d91b0a6eb7ca841ac5753ca75866e3d5d75c"
	expectedName = ""
	object       = "core"
)

func TestProcessEvent(t *testing.T) {

	type testCase struct {
		changes  []docker.Change
		client   mockClient
		status   string
		expected result
		conf     Conf
		Name     string
		Object   string
	}

	var expectedParams Item
	expectedNotification := false
	var startTime time.Time
	hostname, err := os.Hostname()
	if err != nil {
		t.Errorf("Cannot get hostname")
	}
	sp := mocks.NewSyncProducer(t, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dump, err := httputil.DumpRequest(r, true)
		if err != nil {
			t.Errorf("Cannot dump rest request: %s", err)
			return
		}
		t.Logf("Received REST request: %s", dump)
		decoder := json.NewDecoder(r.Body)
		if r.Method != "POST" {
			t.Errorf("Wrong request method: expected POST, received %s", r.Method)
		}
		var params Item
		err = decoder.Decode(&params)
		if err != nil {
			t.Error("Impossible to decode request")
		}
		currentTime := time.Now()
		if params.Timestamp.Before(startTime) || params.Timestamp.After(currentTime) {
			t.Errorf("Wrong Timestamp: expected between (%s) and (%s), received (%s)", startTime, currentTime, params.Timestamp)
		}

		if expectedNotification == false {
			t.Errorf("Received an unexpected REST notification: %v", params)
		}

		params.Timestamp = time.Time{} //Cannot forsee event time, we disable check
		if !(reflect.DeepEqual(params, expectedParams)) {
			t.Errorf("Wrong parameters: expected %v, received %v", expectedParams, params)
		}
		expectedNotification = false
	}))
	defer ts.Close()

	testCases := []testCase{
		/*{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
					{
						Path: "core.1",
						Kind: 1,
					},
				},
				ChangesErr: errors.New("Failed to get changes"),
				CommitErr:  nil,
				PushErr:    nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{

				ChangesCalled: true,
				CommitCalled:  false,
				PushCalled:    false,
			},
			conf: Conf{
				Registry:    "store.com:1111",
				KafkaServer: "kafka:9092",
				RestURI:     "",
				Hostname:    hostname,
				Producer:    sp,
			},
			Name:   "registry.com/bbb/ccc:1.0.1",
			Object: "core",
		},*/
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
					{
						Path: "core.1",
						Kind: 1,
					},
				},
				ChangesErr: nil,
				CommitErr:  errors.New("Commit failed"),
				PushErr:    nil,
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{
				ChangesCalled: true,
				CommitCalled:  true,
				PushCalled:    false,
				RemoveCalled:  false,
			},
			conf: Conf{
				Registry:    "registry.com",
				KafkaServer: "",
				RestURI:     ts.URL,
				Hostname:    hostname,
			},
			Name:   "registry.com/bbb/ccc:1.0.2",
			Object: "core",
		},
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
					{
						Path: "core.1",
						Kind: 1,
					},
				},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    errors.New("Push failed"),
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{

				ChangesCalled: true,
				CommitCalled:  true,
				PushCalled:    true,
				RemoveCalled:  false,
			},
			conf: Conf{
				Registry:    "store.com:1111",
				KafkaServer: "kafka:9092",
				RestURI:     "",
				Hostname:    hostname,
				Producer:    sp,
			},
			Name:   "registry.com/bbb/ccc:1.0.1",
			Object: "core",
		},
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
					{
						Path: "core.1",
						Kind: 1,
					},
				},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    nil,
				RemoveErr:  errors.New("Remove failed"),
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{

				ChangesCalled: true,
				CommitCalled:  true,
				PushCalled:    true,
				RemoveCalled:  true,
			},
			conf: Conf{
				Registry:    "store.com:1111",
				KafkaServer: "kafka:9092",
				RestURI:     "",
				Hostname:    hostname,
				Producer:    sp,
			},
			Name:   "registry.com/bbb/ccc:1.0.1",
			Object: "core",
		},
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
					{
						Path: "core.1",
						Kind: 1,
					},
				},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    nil,
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{

				ChangesCalled: true,
				CommitCalled:  true,
				PushCalled:    true,
				RemoveCalled:  true,
			},
			conf: Conf{
				Registry:    "store.com:1111",
				KafkaServer: "kafka:9092",
				RestURI:     "",
				Hostname:    hostname,
				Producer:    sp,
			},
			Name:   "registry.com/bbb/ccc:1.0.1",
			Object: "core",
		},
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 1,
					},
					{
						Path: "java_pid239.hprof",
						Kind: 1,
					},
				},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    nil,
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{
				ChangesCalled: true,
				CommitCalled:  true,
				PushCalled:    true,
				RemoveCalled:  true,
			},
			conf: Conf{
				Registry:    "store.com",
				KafkaServer: "kafka:9092",
				RestURI:     ts.URL,
				Hostname:    hostname,
				Producer:    sp,
			},
			Name:   "ccc:latest",
			Object: "heapdump",
		},
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
				},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    nil,
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{
				ChangesCalled: true,
				CommitCalled:  false,
				PushCalled:    false,
				RemoveCalled:  false,
			},
			conf: Conf{
				Registry:    "store.com",
				KafkaServer: "kafka:9092",
				RestURI:     "",
				Hostname:    hostname,
			},
			Name: "registry.com/bbb/ccc:1.0.3",
		},
		{
			client: mockClient{
				Changes:    []docker.Change{},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    nil,
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "start",
			expected: result{
				ChangesCalled: false,
				CommitCalled:  false,
				PushCalled:    false,
				RemoveCalled:  false,
			},
			conf: Conf{
				Registry:    "store.com",
				KafkaServer: "",
				RestURI:     "",
				Hostname:    hostname,
			},
			Name: "registry.com/bbb/ccc:1.0.4",
		},
		{
			client: mockClient{
				Changes: []docker.Change{
					{
						Path: "/tmp/toto.txt",
						Kind: 2,
					},
					{
						Path: "core.1",
						Kind: 1,
					},
				},
				ChangesErr: nil,
				CommitErr:  nil,
				PushErr:    nil,
				RemoveErr:  nil,
				Res: result{
					ChangesCalled: false,
					CommitCalled:  false,
					PushCalled:    false,
					RemoveCalled:  false,
				},
				Testing: t,
			},
			status: "die",
			expected: result{
				ChangesCalled: true,
				CommitCalled:  false,
				PushCalled:    false,
				RemoveCalled:  false,
			},
			conf: Conf{
				Registry:    "", //empty registry should not commit nor push
				KafkaServer: "kafka:9092",
				RestURI:     ts.URL,
				Hostname:    hostname,
				Producer:    sp,
			},
			Name:   "registry.com/bbb/ccc:1.0.5",
			Object: "core",
		},
	}

	for _, tc := range testCases {
		startTime = time.Now()
		if tc.expected.CommitCalled {
			expectedName = computeCommitName(tc.conf.Registry, tc.Name)
		} else {
			expectedName = tc.Name
		}
		if len(tc.Object) != 0 {
			if len(tc.conf.RestURI) != 0 {
				expectedParams = Item{Pushed: tc.expected.PushCalled, Name: expectedName, Tag: id[:12], Hostname: hostname, Object: tc.Object, Version: notificationVersion}
				expectedNotification = true
			}
			if len(tc.conf.KafkaServer) != 0 {
				pattern := fmt.Sprintf("{\"version\":\"%s\",\"timestamp\":\".+\",\"pushed\":%t,\"name\":\"%s\",\"tag\":\"%s\",\"hostname\":\"%s\",\"object\":\"%s\"}",
					notificationVersion, tc.expected.PushCalled && tc.client.PushErr == nil, expectedName, id[:12], tc.conf.Hostname, tc.Object)
				sp.ExpectSendMessageWithCheckerFunctionAndSucceed(generateRegexpChecker(pattern))
			}
		}

		processEvent(&docker.APIEvents{Status: tc.status, ID: id, From: tc.Name}, tc.conf, &tc.client)
		if !(reflect.DeepEqual(tc.expected, tc.client.Res)) {
			t.Errorf("Expected %#v found %#v", tc.expected, tc.client.Res)
		}

		if !(reflect.DeepEqual(tc.expected, tc.client.Res)) {
			t.Errorf("Expected %#v found %#v", tc.expected, tc.client.Res)
		}

		if expectedNotification {
			t.Errorf("Expected REST notification (%v) not received for test case (%#v)", expectedParams, tc.expected)
		}
	}

	if err := sp.Close(); err != nil {
		t.Error(err)
	}
}
