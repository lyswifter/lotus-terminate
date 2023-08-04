# Filecoin's sector terminating simulator
This is the command-line tool for estimating how many pledge FILs will remain if we terminate all sectors early.

## Summary
This repository contains necessary code to calculating the pledge penalty when you try to terminate sectors on the lastest tipset height. It will reconstruct current state of power actor and reward actor base on the specified chain height. Then it just call for  miner actor's ```PledgePenaltyForTermination``` function to estimate necessary penalty fee like the actual onchain action does.

Though we just used the version of ```v7/actors``` code lib, it is correct for us to get what we want, because there is no consensus changes in this part of builtin actors.

## Usage
You just need copy-and-paste this file to your ```cmd/lotus``` directory and make sure you could compile it, then you can get the ```lotus``` binary with the additinal command ```terminateAll```.

Using the following command to estimate remaining FILs for you.
```./lotus terminateAll balance --actor f0xxx```

