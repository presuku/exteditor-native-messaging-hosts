#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

rm -rf $SCRIPT_DIR/../output

make -C $SCRIPT_DIR/../native clean
make -C $SCRIPT_DIR/../native pkg
mkdir -p $SCRIPT_DIR/../output

cp -r $SCRIPT_DIR/../native/output/{windows_amd64,linux_amd64} $SCRIPT_DIR/../output/
cp $SCRIPT_DIR/../packaging/*.bat $SCRIPT_DIR/../output/windows_amd64
cp $SCRIPT_DIR/../packaging/*.sh $SCRIPT_DIR/../output/linux_amd64
cp $SCRIPT_DIR/../packaging/exteditor.json.in $SCRIPT_DIR/../output/windows_amd64
cp $SCRIPT_DIR/../packaging/exteditor.json.in $SCRIPT_DIR/../output/linux_amd64

cd $SCRIPT_DIR/../output

zip -r exteditor-windows_amd64.zip windows_amd64
zip -r exteditor-linux_amd64.zip linux_amd64
