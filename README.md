![Choria Stream Replicator](https://choria-io.github.io/stream-replicator/logo.png)

## Overview

This is a Stream data replicator for [Choria Streams](https://choria.io/docs/streams/) aka [NATS JetStream](https://docs.nats.io/jetstream/).

Conceptually it's like a [JetStream Source](https://docs.nats.io/running-a-nats-service/nats_admin/jetstream_admin/replication)
that runs outside the JetStream process and can therefore be used to copy data between 2 completely independent clusters.

In addition to the core Source-like behavior this replicator has some features of particular use to the Choria project in 
that it can sample a Source Stream and, for any unique publisher, only replicate data on a set interval like once an hour
or when there is a significant change in the data being sent. 

When sampling it publishes advisories about senders that have not been seen recently and other significant events 
allowing a central aggregator to have a more granular time window awareness than it could get by looking at its data alone.

![Deployment](https://choria-io.github.io/stream-replicator/deployment.png)

Multiple Streams can be replicated and order is preserved.

 * [Documentation](https://choria-io.github.io/stream-replicator/)
 * [Discussions](https://github.com/choria-io/stream-replicator/discussions)
 * [Slack](https://slack.puppet.com/) in channel `#choria`

[![Go Report Card](https://goreportcard.com/badge/github.com/choria-io/stream-replicator)](https://goreportcard.com/report/github.com/choria-io/stream-replicator)
[![CodeQL](https://github.com/choria-io/stream-replicator/workflows/CodeQL/badge.svg)](https://github.com/choria-io/stream-replicator/actions/workflows/codeql.yaml)
[![Unit Tests](https://github.com/choria-io/stream-replicator/actions/workflows/test.yaml/badge.svg)](https://github.com/choria-io/stream-replicator/actions/workflows/test.yaml)
