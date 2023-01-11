+++
title = "Copying Samples of Data"
toc = true
weight = 30
pre = "<b>2.3. </b>"
+++

## Overview

It's often a case that data in a specific location is produced frequently for maximal correctness of local data sources but in an archive or system that calls into those locations data freshness is not the most important property.

Stream Replicator supports sampling data from the Source and only sending a subset of data to the Target which can greatly improve the efficiency wrt network use and resources in the central archive.

We show JSON payload based sampling below, however, since version `0.0.6` we can also sample on Header values and Subjects or parts of Subjects.

Given data like this sent from every node in the fleet at a 5 minute interval:

```json
{
  "sender":"some.host.example.net",
  "metadata": {}
}
```

We want to copy the message to target only when:

* `some.host.example.net` was seen for the first time
* If the payload changes significantly in size
* Once per hour

We optionally want to raise advisories when:

* A `new` advisory the first time `some.host.example.net` was seen
* Should `some.host.example.net` not be seen for 12 minutes send a `timeout` warning advisory
* Should `some.host.example.net` not be seen for over an hour send an `expire` advisory and treat it again as new should it return
* Should `some.host.example.net` return between `expire` and `timeout` send a `recovery` advisory and resume normal hourly samples

Together this allows the central archive to have fresh data for any new node or node that started gathering metadata after being new and a near real time view of availability of any specific node with enough information to maintain it's local cache of data.

You can combine this feature with [age limits](../all/#skipping-old-messages).

## Configuration

We will start with the configuration from [Copying all messages](../all) and we suggest you read that section first.

```yaml
streams:
  - stream: NODE_DATA
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
```

### Sampling

Lets configure the Replicator to start sampling the above configuration on the `sender` field in the payload:

```yaml
streams:
  - stream: NODE_DATA
    source_url: nats://nats.us-east.example.net:4222
    target_url: nats://nats.central.example.net:4222
    inspect_field: sender
    inspect_duration: 1h
    warn_duration: 12m
    size_trigger: 1024
    advisory:
       subject: NODE_DATA_ADVISORIES
       reliable: true
```

This configures the Replicator as described above:

| Setting                 | Description                                                                                                     |
|-------------------------|-----------------------------------------------------------------------------------------------------------------|
| `inspect_field`         | A JSON key to extract from the payload and key inspection off that. Data without this key will always be copied |
| `inspect_header`        | Inspects the value of a Header in the NATS message. Since version `0.0.6`                                       |
| `inspect_subject_token` | Inspects based on a specific token in the subject or the full subject when set to -1. Since version `0.0.6`     |
| `inspect_duration`      | The sample frequency and also the longest time we will keep awareness of this node, data will be copied hourly  |
| `warn_duration`         | Warn using an advisory when a node was not seen for 12 minutes                                                  |
| `size_trigger`          | Should the payload grow or shrink by this many bytes trigger an immediate copy                                  |

The `size_trigger` accommodates the typical scenario where full metadata might be gathered some time after a node starts, we'd send initial minimal metadata and, once gathered, a full set.  The first full set message will be copied even if it comes before an hour has passed.

Pick the `inspect_duration` based on your needs but ensure that it is longer than frequency the nodes will publish data at else all data will be copies.

See [Sampling Advisories](../../monitoring/#sampling-advisories) for details about advisories.
