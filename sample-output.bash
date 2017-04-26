#! /usr/bin/env bash
sqlite3 -column -header ${1:-wifi.db} "
	SELECT
		c.timestamp as timestamp,
		a.apname as ap,
		c.clientip as ip,
		c.clientssid as ssid,
		c.clientproto as protocol,
		c.clientrssi as rssi,
		c.clientsnr as snr,
		c.clientrecv as bytesin,
		c.clientsent as bytesout
	FROM
		clients as c
	JOIN aps as a
		ON c.apmac==a.apmac
	ORDER BY
		timestamp ASC,
		ap ASC,
		ssid ASC,
		ip ASC;
"
