+++
title = "Copying All Data"
toc = true
weight = 20
pre = "<b>2.2. </b>"
+++

The most common configuration is simply to copy all messages from a Source to a Target.

We won't show the overall replication configuration, see [Basic Configuration](../basic) for an intro.

## Configuring the Stream

One can configure multiple streams and a Replicator will be configured for each Stream.  Today the only mode we support is single worker, order preserving, copy from one Stream to another.

## Sources and Targets

A Source is where the messages are and a Target is where they are being copied.

### Replicator Placement

Generally for best performance when copying all data like this it is best to place the Replicator in the target and setting `target_initiated: true`. If instead you cannot deploy it in the target you can set this to false (or don't set it) which would result in a push like behavior.

{{% notice style="warning" %}}
While it will function to copy data with the replicator in the Source it is very exposed to latency and can be extremely slow over long links.
{{% /notice %}}

### Copying Entire Streams

The settings below will copy `NODE_DATA` from `nats.us-east.example.net` to `nats.central.example.net`, both streams should exist, and it must be called `NODE_DATA` on both sides.

```yaml
streams:
  - stream: NODE_DATA
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    target_initiated: true
```

### Copying to differently named Streams

The target stream name can be set to a different name than the source.

```yaml
streams:
  - stream: NODE_DATA
    target_stream: FLEET_NODES
    target_subject_prefix: FLEET_NODES
    target_subject_remove: NODE_DATA
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    target_initiated: true
```

We also show the optional `target_subject_prefix` and `target_subject_remove` settings. This will prepend the prefix to the subjects in the source stream and remove the duplication.  So if the source was `NODE_DATA.host.example.net` then the subjects in the target would be `FLEET_NODES.host.example.net`.

### Setting initial starting location

One might want to avoid copying the entire stream from Source to Target, especially when first setting up replication between existing locations.

We support the following settings to influence starting point:

| Setting          | Description                                                                                  | Example                     |
|------------------|----------------------------------------------------------------------------------------------|-----------------------------|
| `start_sequence` | A specific message sequence to start at in the Source stream                                 | `1024`                      |
| `start_time`     | A specific start time to start at in the Source stream, has to be RFC3339 format             | `2006-01-02T15:04:05Z07:00` |
| `start_delta`    | Calculates a relative start time using this delta, supports `h`, `d`, `w`, `M` and `Y` units | `1w`                        |
| `start_at_end`   | Sends the next message that arrives as the first one                                         | `true`                      |

### Skipping old messages

While setting the initial starting location can let you avoid old data at initial start, later if the replicator is down for a while you might find you are traversing ancient data while catching up.

To avoid replicating old data you can set a Maximum Age using the `max_age: 1h` property, to always in all circumstances skip old messages from the source stream.

### Filtering the Source

If your source stream has subjects like `fleet.REGION.COUNTRY.CITY.>` and you want to copy only the Paris related data into your Target you can do so by setting a filter subject like `filter_subject: fleet.eu.fr.cdg.>`. The target stream will now have all the matching only those subjects, ideal for branch scenarios.

```yaml
streams:
  - stream: NODE_DATA
    target_stream: FLEET_NODES
    target_subject_prefix: FLEET_NODES
    target_subject_remove: NODE_DATA
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    target_initiated: true
    filter_subject: sku.eu.fr.cdg.>
    start_sequence: 1
```
