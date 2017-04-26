#! /usr/bin/env bash
sqlite3 -column -header ${1:-wifi.db} "
	SELECT
		DATETIME(c.timestamp) as timestamp,
		c.clientip as ip,
		a.apname as ap,
		c.clientssid as ssid,
		CASE c.clientproto
			WHEN 1 THEN a.apchannel5
			WHEN 7 THEN a.apchannel5
			ELSE a.apchannel24
		END as channel,
		c.clientrssi as rssi,
		c.clientsnr as snr,
		c.clientrecv/1000000 as MBrecv,
		c.clientsent/1000000 as MBsent
	FROM
		clients as c
	JOIN aps as a
		ON c.apmac = a.apmac
	WHERE
		c.timestamp = a.timestamp
	ORDER BY
		timestamp ASC,
		ip ASC,
		ap ASC,
		ssid ASC;
"
