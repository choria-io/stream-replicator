+++
title = "Basic Configuration"
toc = true
weight = 10
pre = "<b>2.1. </b>"
+++

This section covers the basic information needed for copying streams from a Source to a Target.

## Placement Architecture

Generally it is best to place the Replicator nearest in terms of network latency to the Target. 

When sampling data or tracking and publishing advisories it should be placed nearest to the Source. Advisories are also only Published to the Source so we'd want to give those the best chance of success possible by eliminating network issues.

See details in each deployment scenario for further details and placement constraints.

## Requirements

Both the Source and Target Stream has to exist before this tool will run. The 2 streams do not need to have the same configuration, for example one can keep data for an hour the other for a month.  Or one can be Memory based while the other File.

If the Source does not exist the Replicator will keep trying until it does and then start.  If the Target does not exist once the Source is found the Target will be created using the same configuration as the Source.

For now, we assume like-for-like streams in 2 separate servers.

## Basic Configuration

The Stream Replicator itself has some configuration to set, this is going to be the same for all modes of copy.

```yaml
name: US-EAST
monitor_port: 8080
loglevel: info
state_store: /var/lib/stream-replicator
logfile: /var/log/stream-replicator.log
streams: [] # see other documentation pages
```

This is fairly self-explanatory except for the `name` property: typically one would deploy a tree-like structure of many Streams replicated into one, perhaps many data centers all having node events.

To assist with configuration all of these locations would have the same Stream name and same subjects. But centrally one might want to know where a specific message is from.

Setting a unique per-location name like `US-EAST` will add a header to every copied message that will include this name.

Loglevels can be `debug`, `info` (default) or `warn` and if `monitor_port` is not set (default) Prometheus metrics will not be exposed.

The remaining settings is obvious and match what is in the RPM packages.

## NATS Credentials

We support using NATS credentials, JWT and NKey files for authentication by adding parameters to any nats source or target urls:

| Description           | Example                                                           |
|-----------------------|-------------------------------------------------------------------|
| Username and password | `nats://user:pass@example.net:4222`                               |
| Credentials file      | `nats://example.net:4222?credentials=/path/to/creds`              |
| JWT and NKey files    | `nats://example.net:4222?jwt=/path/to/my.jwt&nkey=/path/to/my.nk` |

## TLS

TLS is supported, one can have per Target or Source settings.  Per Stream settings or per Replicator settings.  The most specific will be used for example, given this partial configuration file:

```yaml
tls:
  ca: /path/to/ca.pem
  cert: /path/to/cert.pem
  key: /path/to/key.pem
streams:
  - first:
      target_tls:
        ca: /path/to/first-ca.pem
        cert: /path/to/first-cert.pem
        key: /path/to/first-key.pem
  - second:
      target_tls:
        ca: /path/to/second-ca.pem
        cert: /path/to/second-cert.pem
        key: /path/to/second-key.pem
 
```

Here we have a Replicator-wide default referencing `ca.pem`, `cert.pem` and `key.pem`.  All Sources will use this, all Targets without `target_tls` will use this.  But those targets with specific ones will use those.

This is based on the assumption that one might copy many data from 1 single JetStream server to many locations.  This model allows one to state the source TLS once only for all streams.

Source specific TLS can be set with `source_tls`.  At a Stream level one can also set `tls` to have the same TLS settings used for Source and Target.

## Choria JWT Tokens

Choria Broker supports running in a mode that requires Choria specific JWT tokens and private keys in order to connect to it. Replicator supports these. One can have per Target or Source settings.  Per Stream settings or per Replicator settings.  The most specific will be used for example, given this partial configuration file:

```yaml
choria:
  seed_file: /etc/stream-replicator/choria.seed
  jwt_file: /etc/stream-replicator/choria.jwt
  collective: choria
streams:
  - first:
      target_choria:
          seed_file: /etc/stream-replicator/first.seed
          jwt_file: /etc/stream-replicator/first.jwt
          collective: choria
  - second:
      target_choria:
        seed_file: /etc/stream-replicator/second.seed
        jwt_file: /etc/stream-replicator/second.jwt
        collective: choria 
```

Here we have a Replicator-wide default referencing `choria.seed`, `choria.jwt` and `choria` collective.  All Sources will use this, all Targets without `target_tls` will use this.  But those targets with specific ones will use those.

This is based on the assumption that one might copy many data from 1 single JetStream server to many locations.  This model allows one to state the source connection details once only for all streams.

Source specific Choria connection details can be set with `source_choria`.  At a Stream level one can also set `choria` to have the same TLS settings used for Source and Target.
