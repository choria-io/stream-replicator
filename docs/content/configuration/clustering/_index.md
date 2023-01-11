+++
title = "HA Clustering"
toc = true
weight = 40
pre = "<b>2.4. </b>"
+++

## Overview

In all scenarios the Replicator supports running in highly available clustered modes. 

Generally the replicator is designed to be strictly ordered, this means having active-active workers on a single stream
is not possible without losing ordered messages. In this scenario we support partitioning streams and scaling out the
copier workers horizontally and vertically with HA failover between nodes.

Combined this allows a very reliable and high performant replication infrastructure to be built.

## Leader Election based Failover

The most basic HA configuration is to let multiple instances of the same stream replication profile failover between the
instances managing that replication.

To perform election a NATS Key-Value bucket called `CHORIA_LEADER_ELECTION` must be configured. If you are using Choria Streams
this will already exist.  Else it needs to be made:

```nohighlight
$ nats kv add CHORIA_LEADER_ELECTION --ttl 10s --replicas 3
```

{{% notice style="note" %}}
We strongly suggest that this bucket is made on a highly available NATS cluster with at least 3 nodes.
{{% /notice %}}

Now when configuring the replication configuration for a stream we can set it to use this election bucket to campaign
for a leadership:

```yaml
streams:
  - stream: NODE_DATA
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    target_initiated: true
    leader_election_name: NODE_DATA
```

With this in place you can simply start any number of replicators and they will elect a leader who will own copying the
data.  Should that leader fail another one will step in after roughly 30 seconds.

## Message Partitioning

The previous section showed how Leader Election can be used to pick a single node to replicate the data it does mean
one node has to do all the copying.

We can employ [Subject Partitioning](https://docs.nats.io/nats-concepts/subject_mapping) to split our work into partitions
and then run a replicator per partition.  With each partition being highly available.  This way we can have multiple active
workers all running in a HA cluster.

If we assume we have server metadata published to `NODE_DATA.node1.example.net` we could configure the NATS Servers
to perform mapping such that based on `node1` in the FQDN a deterministic partition is picked.  This will mean that every
message for `node1.example.net` will always end up in the same partition and so ordered replication will keep things valid
on that dimension.

Let's look how we would partition that data into 2 partitions, we'll show how to do it in either NATS Server or Choria Streams:

NATS Configuration:

```nohighlight
accounts {
  "test": {
    "mappings":{
      "NODE_DATA.*.>": "NODES.US-EAST-1.{{partition(2,1)}}.{{wildcard(1)}}.>"
    }
  }
}
```

Choria Configuration:

```ini
plugin.choria.network.mapping.names = nodes
plugin.choria.network.mappin.nodes.source = NODE_DATA.*.>
plugin.choria.network.mappin.nodes.destination = NODES.US-EAST-1.{{partition(2,1)}}.{{wildcard(1)}}.>
```

Let's see what that will do to our subjects.

```nohighlight
$ nats server mappings 'NODE_DATA.*.>' 'NODES.US-EAST-1.{{partition(2,1)}}.{{wildcard(1)}}.>'
Enter subjects to test, empty subject terminates.

? Subject NODE_DATA.node3.example.net
NODES.US-EAST-1.0.node3.example.net

? Subject NODE_DATA.node1.example.net
NODES.US-EAST-1.0.node1.example.net

? Subject NODE_DATA.node2.example.net
NODES.US-EAST-1.1.node2.example.net

? Subject NODE_DATA.node3.example.net
NODES.US-EAST-1.0.node3.example.net
```

We can see that the messages are mapped from `NODE_DATA.<FQDN>>` to `NODES.US-EAST-1.<PARTITION>.<FQDN>` and further
that the same node always land in the same partition (see `node3.example.net`).

We would now either create 2 streams, one per partition, or one stream that reads `NODES.US-EAST-1.>`.

{{% notice style="note" %}}
Take care not to overlap the subjects, this is why we rewrite `NODE_DATA.>` to `NODES.>`.
{{% /notice %}}

From this point we simply create a normal leader elected stream configuration that has a subject filter.

```yaml
streams:
  - stream: NODE_DATA_P0
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    target_initiated: true
    leader_election_name: NODE_DATA_P0
    filter_subject: NODES.US-EAST-1.0.>

  - stream: NODE_DATA_P1
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    target_initiated: true
    leader_election_name: NODE_DATA_P1
    filter_subject: NODES.US-EAST-1.1.>
```

So we created 2 stream replication profiles that each copy only 1 partition. We set unique leader election names per
partition.

So end result is that if this replicator runs over 2 nodes the work can spread out over the 2 nodes, fail over, and scale
horizontally.

## HA for Sampling

When [Copying Samples of Data](../sampling) the configuration that needs to be done is identical to the setups
before.  You can combine partitioned and failover with sampling without a problem.

When sampling is active the Replicators will share state within their cluster using a gossip protocol automatically.

Review the [Monitoring Reference](../../monitoring/#viewing-cluster-sync-gossip) for detail on how to view the gossip
messages and to compare the shared state between replicator instances.
