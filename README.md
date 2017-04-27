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
        Path to config file
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
3. Setting a configuration file, e.g. `wifitracker -config myConfigFile`

The format of the configuration file is:

```
# this is a comment
community=cheese
debug=true
```

While not strictly necessary, you use `key=value` syntax, which makes using the same file for Docker much easier. Speaking of which...

## Docker

For those of you with a Docker persuasion, the latest version is always published at [Docker Hub](https://hub.docker.com/r/dotwaffle/wifitracker/) and can be easily pulled with: `docker pull dotwaffle/wifitracker:latest`

The Dockerfile specfies that `/db` is a volume, and that `/db` is the workdir, so the `wifi.db` file will by default be created at `/db/wifi.db`.

Dealing with flags and configuration files in Docker is surprisingly annoying, so in a fit of both rage and madness I decided to encourage you to follow Kelsey Hightower's example, and the example of the [12 Fractured Apps](https://medium.com/@kelseyhightower/12-fractured-apps-1080c73d481c) by using environment variables, by writing your own Dockerfile that uses `FROM dotwaffle/wifitracker:latest` or by doing something at run-time like:

```
docker run -dt --name wifitracker --rm -e COMMUNITY=cheese -e DEBUG=true dotwaffle/wifitracker
```

You can even use your configuration file as an environment variable list!

```
docker run -dt --name wifitracker --rm --env-file wifitracker.conf dotwaffle/wifitracker
```

Now, of course you'll have a problem that your data is being stored at `/db/wifi.db` which is only bound to that particular instance of the container. If you stop/start it, it will not have the old data! Let's create a persistent data container that will be shared among all the times you run this wonderful app:

```
docker create --name wifitracker-data dotwaffle/wifitracker
docker run -dt --name wifitracker --rm --volumes-from wifitracker-data dotwaffle/wifitracker
```

You'll notice there that we mounted the volume that the `wifitracker-data` app has, even though we only created it -- we didn't actually run it! Data should now be persisted. You can clean up that volume easily with `docker rm -v wifitracker-data`. A bright spark may even decide not to use that data container for the volume and instead do `-v myFolder:/db` which would mount local folder `myFolder` on the `/db` location, but I couldn't possibly comment.

Final note: You'll notice that I've specified `docker run -d` which detaches the process. You can watch the progress with `docker logs --follow wifitracker`, you can attach to it with `docker attach wifitracker` (detach again with ^p^q) or you can start it and immediately attach by changing the run parameter to `docker run -it` for interactive.

Trust me: Docker has a really unusual interface that really is developed in a strange way, but once you get used to it, it's pretty handy.

## Using the data

I've supplied `sample-output.bash` which will run a nice little SQL query against your database and spit out the data for you in a nice to digest format. It'll join the two SQL tables nicely so that it'll show which access point and WiFi channel it was on when the scan occurred. Eventually I'll be making a front-end to this app which will plot live where users are, and move them across a map as they move between access-points. Based on my hatred of coding GUIs, this may take considerably longer than this app took.

Enjoy!
