#!/bin/sh -ex

mkdir rootfs
cd rootfs
id=$(docker create busybox)
docker export "$id" | tar x
docker rm "$id"
