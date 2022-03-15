![Choria Stream Replicator](https://choria.io/stream-replicator-logo-horizonal.png)

## Overview

This is a Stream data replicator for [NATS JetStream](https://docs.nats.io/jetstream/) which forms the enabling technology
for [Choria Streams](https://choria.io/docs/streams/).

Conceptually it's like a [JetStream Source](https://docs.nats.io/running-a-nats-service/nats_admin/jetstream_admin/replication)
that runs outside the JetStream process and can therefore be used to copy data between 2 completely independent clusters.

In addition to the core n:1 source behavior this replicator has some features of particular use to the Choria project in 
that it can sample a Source stream and, for any unique publisher, only replicate data on a set interval like once an hour
or when there is a significant change in the data being sent. When sampling it publishes advisories about senders that 
have not been seen recently and other significant events allowing a central aggregator to have a more granular time window 
awareness than it could get by looking at it's data alone.

## Relation to Version 1

Previously a Stream Replicator projected existed here that support NATS Streaming Server.  This project has not been archived
and completely rewritten around JetStream.

The archive is the [choria-legacy/stream-replicator](https://github.com/choria-legacy/stream-replicator) project.

## Status

This is a work in progress, it is feature complete and has extensive tests but documentation and more will come.
