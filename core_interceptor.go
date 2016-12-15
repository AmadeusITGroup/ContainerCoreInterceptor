package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"github.com/coreos/go-systemd/journal"
	"github.com/franela/goreq"
	"github.com/fsouza/go-dockerclient"
	"github.com/golang/glog"
)

const notificationVersion = "1.0"

//ClientInterface is used to mock the docker client for unit testing
type ClientInterface interface {
	ContainerChanges(id string) ([]docker.Change, error)
	CommitContainer(opts docker.CommitContainerOptions) (*docker.Image, error)
	PushImage(opts docker.PushImageOptions, auth docker.AuthConfiguration) error
	RemoveImage(name string) error
}

func processEvent(event *docker.APIEvents, conf Conf, client ClientInterface) {
	if event.Status != "die" {
		return
	}
	glog.V(1).Infof("Container %s has died\n", event.ID)
	changes, err := client.ContainerChanges(event.ID)
	if err != nil {
		glog.Fatal(err)
	}
	glog.V(3).Infoln("Changes:")
	for _, change := range changes {
		glog.V(3).Infof("\t- %#v\n", change)

		object := ""
		if change.Kind != docker.ChangeAdd {
			continue
		}
		switch {
		case strings.Contains(change.Path, ".hprof"):
			object = "heapdump"
		case strings.Contains(change.Path, "core."):
			object = "core"
		}
		if len(object) == 0 {
			continue
		}
		glog.V(2).Infof("Container %s has generated a %s\n", event.ID, object)
		pushed, name, tag := pushContainer(event, conf, client)
		timestamp := time.Now() //Notification time set only once here

		if len(conf.KafkaServer) != 0 {
			notifyKafka(conf, pushed, name, tag, object, timestamp)
		}
		if len(conf.RestURI) != 0 {
			notifyRest(conf, pushed, name, tag, object, timestamp)
		}
		return
	}
}

func computeCommitName(registry string, from string) string {
	pos := strings.Index(from, "/")

	if pos == -1 {
		return registry + "/" + strings.Replace(from, ":", "_", -1)
	}
	return registry + strings.Replace(from[pos:], ":", "_", -1)
}

func logError(message string) {
	err := journal.Send(message, journal.PriErr, nil)
	if err != nil {
		glog.Errorf("Couldn't log to journal: " + message + ": error while writing: " + err.Error())
		return
	}
	glog.Errorf(message)
}

func pushContainer(event *docker.APIEvents, conf Conf, client ClientInterface) (pushed bool, name, tag string) {
	pushed = false
	tag = event.ID[:12]
	if len(conf.Registry) == 0 {
		name = event.From
		return
	}
	name = computeCommitName(conf.Registry, event.From)
	image, err := client.CommitContainer(docker.CommitContainerOptions{Container: event.ID, Repository: name, Tag: tag})
	if err != nil {
		logError("Couldn't commit container " + event.ID + ": " + err.Error())
		return
	}
	glog.V(2).Infof("Container %s has been commited as %s:%s\n", event.ID, name, tag)
	err = client.PushImage(docker.PushImageOptions{Name: name, Tag: tag}, docker.AuthConfiguration{})
	if err != nil {
		logError("Couldn't push image " + name + ":" + tag + ": " + err.Error())
		return
	}
	glog.V(2).Infof("Container %s has been pushed as %s:%s\n", event.ID, name, tag)
	pushed = true
	err = client.RemoveImage(image.ID)
	if err != nil {
		logError("Couldn't delete image " + image.ID + ": " + err.Error())
		return
	}
	glog.V(2).Infof("Image %s has been deleted\n", image.ID)
	return
}

func connectToKafka(conf *Conf) error {
	var err error
	conf.Producer, err = sarama.NewSyncProducer([]string{conf.KafkaServer}, nil)
	if err != nil {
		logError("Couldn't open the connection to the kafka server " + conf.KafkaServer + " - " + err.Error())
	}
	return err
}

func closeKafkaConnection(conf *Conf) {
	if err := conf.Producer.Close(); err != nil {
		logError("Couldn't close the connection to the kafka server " + conf.KafkaServer + " - " + err.Error())
		return
	}
}

func notifyKafka(conf Conf, pushed bool, name string, tag string, object string, timestamp time.Time) error {
	item := Item{Version: notificationVersion, Timestamp: timestamp, Pushed: pushed, Name: name, Tag: tag, Hostname: conf.Hostname, Object: object}
	topic := "events.cores"
	val, err := json.Marshal(item) //We drop the error as the  object is defined just above
	if err != nil {
		logError("Failed to generate the notification message: " + err.Error())
		return err
	}

	msg := &sarama.ProducerMessage{Topic: topic, Value: sarama.StringEncoder(val)}
	partition, offset, err := conf.Producer.SendMessage(msg)
	if err != nil {
		logError("FAILED to send message to " + conf.KafkaServer + ": - " + err.Error())
		return err
	}
	glog.V(2).Infof("> message sent to topic %s, partition %d at offset %d\n", topic, partition, offset)
	return nil
}

func notifyRest(conf Conf, pushed bool, name string, tag string, object string, timestamp time.Time) error {
	item := Item{Version: notificationVersion, Timestamp: timestamp, Pushed: pushed, Name: name, Tag: tag, Hostname: conf.Hostname, Object: object}

	res, err := goreq.Request{
		Method:  "POST",
		Uri:     conf.RestURI,
		Timeout: 500 * time.Millisecond,
		Body:    item,
	}.Do()

	if err != nil {
		logError("Couldn't notify the rest server - " + err.Error())
		if serr, ok := err.(*goreq.Error); ok {
			if serr.Timeout() {
				logError("TIMEOUT occurred - " + err.Error())
			}
		}
		return err
	}

	if res.StatusCode != 200 && res.StatusCode != 220 {
		err := errors.New("Failed to send the REST the notification message, status code: " + strconv.Itoa(res.StatusCode))
		logError(err.Error())
		return err
	}
	return nil
}

type Conf struct {
	Registry       string
	KafkaServer    string
	RestURI        string
	Hostname       string
	DockerEndPoint string
	Producer       sarama.SyncProducer
}

//Item is used to store the body values for REST notificiation
type Item struct {
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Pushed    bool      `json:"pushed"`
	Name      string    `json:"name"`
	Tag       string    `json:"tag"`
	Hostname  string    `json:"hostname"`
	Object    string    `json:"object"`
}

func initFlags() Conf {
	var conf Conf
	flag.StringVar(&conf.Registry, "registry", "", "Docker registry to store the cored containers")
	flag.StringVar(&conf.KafkaServer, "kafkaServer", "", "Kafka server where the core message will be posted")
	flag.StringVar(&conf.RestURI, "restURI", "", "Rest URI where the core dump message will be posted")
	flag.StringVar(&conf.DockerEndPoint, "dockerEp", "unix:///var/run/docker.sock", "Docker events enpoint to listen to")
	flag.Parse()
	if len(conf.Registry) == 0 {
		glog.V(1).Infof("No registry specified, the cored containers will not be stored!\n")
	}
	return conf
}

func main() {
	conf := initFlags()

	client, err := docker.NewClient(conf.DockerEndPoint)
	if err != nil {
		logError("Failed to create client: " + err.Error())
		return
	}

	conf.Hostname, err = os.Hostname()
	if err != nil {
		conf.Hostname = "Unknown"
	}

	client.SkipServerVersionCheck = true

	listener := make(chan *docker.APIEvents, 10)
	defer func() {
		time.Sleep(10 * time.Millisecond)
		if err := client.RemoveEventListener(listener); err != nil {
			logError("Failed to remove event lister: " + err.Error())
		}
	}()

	err = client.AddEventListener(listener)
	if err != nil {
		logError("Failed to add event listener: " + err.Error())
		return
	}
	backoff := 1

	if len(conf.KafkaServer) != 0 {
		if err := connectToKafka(&conf); err != nil {
			return
		}
		defer closeKafkaConnection(&conf)
	}

	for {
		select {
		case msg, ok := <-listener:
			if !ok {
				if backoff > 10 {
					logError("EXIT: Listen on Docker socket timed out, check if docker is started and you have the right to access the socket.")
					os.Exit(1)
				}
				logError("Failed to listen on Docker socket, trying again in" + strconv.Itoa(backoff) + "seconds.")
				time.Sleep(time.Duration(backoff) * time.Second)
				backoff = backoff * 2
			} else {
				glog.V(3).Infof("Received: %v\n", *msg)
				processEvent(msg, conf, client)
			}
		}
	}
}
