# core_interceptor

`Core_interceptor`  can be used to handle core dumps in a dockerized
environment.

It listens on the local docker daemon socket for events. When it receives a
`die` event it checks if the dead container produced any core dump or java
heap dump.  Then the container is committed as `IMAGE_NAME:TAG`:

* `IMAGE_NAME` is composed by the name and tag (if any) of the dead container 
  source image separated by "_".  `TAG` is composed by the first 12
  characters of the dead container ID

The resulting image is then pushed to a configurable Docker registry, and
deleted from the local node.  Finally a notification can be sent to a Kafka
cluster or to a REST endpoint.

## Kafka Notification

The notification content is:

```
{version:1.0, timestamp: now ,pushed: PUSHED, name: IMAGE_NAME, tag: TAG, hostname: HOST, object: OBJECT}
```

* `PUSHED` is a flag telling if the creation/push of the image succeed
* `HOST` is the name of the machine where the container was running
* `OBJECT` is the type of object produced, e.g., `core`

The notification is posted to the topic `cores`.

## Rest API call

You may optionally provide an HTTP URI: the core interceptor will perform a POST
to the given URI. The body of the request will be the same context as in the
Kafka notification.

## Prerequisites

To run this program you need:

* a running docker deamon
* write access to the docker socket
* access to a docker registry with rights to push images (optional)
* write access to a Kafka cluster (optional)

To build this program you need:

* golang compiler
* go tools

## Build

```bash
go build
```

## Usage

The program should be run by a **user** with the **rights to access to the
docker** socket (e.g., root)

```bash
./core_interceptor -h
```

## Logging

The logging is configurable via command line and uses the
[glog](https://github.com/golang/glog) package.
**Core interceptor errors are always logged to the system journal**.
You can get them by running

```bash
journalctl _COMM=core_intercepto
```

##Licence

[MIT License](LICENCE)
