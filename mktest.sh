#!/bin/bash

for i in {1..10};
do
    echo $i;
    mkdir $i;
    for j in {1..10000};
    do
        let "x = 4 + 4 * ($RANDOM % 4)"
        dd if=/dev/urandom of=$i/$j bs=${x}k count=1 &> /dev/null;
    done
done

