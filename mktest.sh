#!/bin/bash

for i in {1..1000};
do
    echo $i;
    mkdir $i;
    for j in {1..10000};
    do
        dd if=/dev/urandom of=$i/$j bs=8k count=1 &> /dev/null;
    done
done

