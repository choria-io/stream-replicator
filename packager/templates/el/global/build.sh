#!/bin/bash

#
# Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
#
# SPDX-License-Identifier: Apache-2.0
#

set -x

TARGET_ARCH="{{cpkg_target_arch}}"
TARBALL="{{cpkg_tarball}}"

if [ ! -z $TARGET_ARCH ]
then
  rpmbuild --target "${TARGET_ARCH}" -ta "${TARBALL}"
else
  rpmbuild -ta "${TARBALL}"
fi
