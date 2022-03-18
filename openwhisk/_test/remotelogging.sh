#!/bin/bash

echo '{"ok":true}' >&3
while read line
do
  echo "hello from stdout"
  echo "hello from stderr" 1>&2
  echo "XXX_THE_END_OF_A_WHISK_ACTIVATION_XXX"
  echo "XXX_THE_END_OF_A_WHISK_ACTIVATION_XXX" 1>&2
  echo '{"hello": "markus"}' >&3
done
