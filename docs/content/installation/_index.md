+++
title = "Installation"
toc = true
weight = 10
pre = "<b>1. </b>"
+++

We distribute an RPM or Docker container for the Stream Replicator package. Debian is not supported at present.

## YUM Repository

For RPMs we publish releases but also nightly builds to our repositories.

Users of our Puppet modules will already have these repositories available.

### Release

```ini
[choria_release]
name=Choria Orchestrator Releases
mirrorlist=http://mirrorlists.choria.io/yum/release/el/$releasever/$basearch.txt
enabled=True
gpgcheck=True
repo_gpgcheck=True
gpgkey=https://choria.io/RELEASE-GPG-KEY
metadata_expire=300
sslcacert=/etc/pki/tls/certs/ca-bundle.crt
sslverify=True
```

### Nightly

Nightly releases are named and versioned `stream-replicator-0.99.0.20221109-1.el7.x86_64.rpm` where the last part of the version is the date.

```ini
[choria_nightly]
name=Choria Orchestrator Nightly
mirrorlist=http://mirrorlists.choria.io//yum/nightly/el/$releasever/$basearch.txt
enabled=True
gpgcheck=True
repo_gpgcheck=True
gpgkey=https://choria.io/NIGHTLY-GPG-KEY
metadata_expire=300
sslcacert=/etc/pki/tls/certs/ca-bundle.crt
sslverify=True
```

## Docker

There is a docker container `choria-io/stream-replicator` that has releases only.
