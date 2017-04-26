# wifitracker

A bad attempt at writing a MAC address tracker for Cisco WiFi APs, by scraping the WLC SNMP tables.

## Usage

In a lame attempt to document, here's the usage statement:

```
$ wifitracker -h
Usage of wifitracker:
  -community string
        SNMP community string (default "public")
  -config string
        Path to config file (default "wifitracker.conf")
  -db string
        Database File (sqlite3) (default "wifi.db")
  -debug
        Turn on debugging output
  -host string
        SNMP host to query (default "127.0.0.1")
  -interval duration
        Polling interval (default 10s)
  -retries int
        SNMP retries (default 1)
  -timeout duration
        SNMP timeout (default 1s)
```

Due to the particular flag package I'm using, you can change any of those options by three methods, in order of precedence:

1. Setting the flag, e.g. `wifitracker -debug -community cheese`
2. Setting an environment variable, e.g. `export COMMUNITY=cheese DEBUG=true; wifitracker`
3. Setting a configuration file, by default it will look at `./wifitracker.conf` if it exists.

The format of the configuration file is:

```
community cheese
debug
```

You can separate with spaces or equals signs, and lines starting with # are ignored.

## Docker

For those of you with a Docker persuasion, the latest version is always published at [Docker Hub](https://hub.docker.com/r/dotwaffle/wifitracker/) and can be easily pulled with: `docker pull dotwaffle/wifitracker`

The Dockerfile specfies that `/db` is a volume, and that `/db` is the workdir. Therefore, if you wanted to provide a config file you could do:

```
docker run -d --name wifitracker --rm -v myConfigFile:/db/wifitracker.conf -t dotwaffle/wifitracker
```

That would mount your file "myConfigFile" in the right place for the app to pick it up. You could also just specify environment variables for the app to pick up instead, by doing something like:

```
docker run -d --name wifitracker --rm -e COMMUNITY=cheese -e DEBUG=true -t dotwaffle/wifitracker
```

Now, of course you'll have a problem that your data is being stored at `/db/wifi.db` which is only bound to that particular instance of the container. If you stop/start it, it will not have the old data! Let's create a persistent data container that will be shared among all the times you run this wonderful app:

```
docker create --name wifitracker-data dotwaffle/wifitracker
docker run -d --name wifitracker --rm --volumes-from wifitracker-data -t dotwaffle/wifitracker
```

You'll notice there that we mounted the volume that the `wifitracker-data` app has, even though we only created it -- we didn't actually run it! Data should now be persisted. You can clean up that volume easily with `docker rm -v wifitracker-data`. A bright spark may even decide not to use that data container for the volume and instead do `-v myFolder:/db` which would mount local folder `myFolder` on the `/db` location, but I couldn't possibly comment.

Final note: You'll notice that I've specified `docker run -d` which detaches the process. You can watch the progress with `docker logs --follow wifitracker`, you can attach to it with `docker attach wifitracker` (detach again with ^p^q) or you can start it and immediately attach by changing the run parameter to `docker run -i` for interactive.

Trust me: Docker has a really unusual interface that really is developed in a strange way, but once you get used to it, it's pretty handy.

Enjoy!
