+++
weight = 5
+++

# Overview

This is a Stream data replicator for [Choria Streams](https://choria.io/docs/streams/) aka [NATS JetStream](https://docs.nats.io/jetstream/).

Conceptually it's like a [JetStream Source](https://docs.nats.io/running-a-nats-service/nats_admin/jetstream_admin/replication)
that runs outside the JetStream process and can therefore be used to copy data between 2 completely independent clusters.

In addition to the core Source-like behavior this replicator has some features of particular use to the Choria project in
that it can sample a Source Stream and, for any unique publisher, only replicate data on a set interval like once an hour
or when there is a significant change in the data being sent.

When sampling it publishes advisories about senders that have not been seen recently and other significant events
allowing a central aggregator to have a more granular time window awareness than it could get by looking at its data alone.

![Deployment](/deployment.png)

Multiple Streams can be replicated and order is preserved.

## Status

This is a work in progress, it is core-feature complete and has extensive tests. For a typical Stream full of Choria 
Registration data produced by Chef Ohai, subject to network latency, this replicator can easily copy 4000 messages / sec 
unsampled and more when sampling is enabled.

## Relation to previous version

Previously a Stream Replicator projected existed here that support NATS Streaming Server.  This project has now been archived
and completely rewritten around JetStream.

The archive is the [choria-legacy/stream-replicator](https://github.com/choria-legacy/stream-replicator) project.
