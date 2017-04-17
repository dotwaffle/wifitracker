package main

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"

	"flag"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/soniah/gosnmp"
)

var (
	community    = flag.String("community", "public", "SNMP community string")
	host         = flag.String("host", "127.0.0.1", "SNMP host to query")
	timeout      = flag.Duration("timeout", 2*time.Second, "SNMP timeout")
	dbFile       = flag.String("db", "wifi.db", "Database File (sqlite3)")
	pollInterval = flag.Duration("interval", 10*time.Second, "Polling interval")
	oids         = [...]string{
		".1.3.6.1.4.1.14179.2.1.4.1.4", // AP MAC List
		".1.3.6.1.4.1.14179.2.2.1.1.3", // AP Names
		".1.3.6.1.4.1.14179.2.1.4.1.2", // Client IP List
		".1.3.6.1.4.1.14179.2.1.4.1.1", // Client MAC List
		".1.3.6.1.4.1.14179.2.1.4.1.7", // Client SSID List
		".1.3.6.1.4.1.14179.2.1.4.1.3", // Client Username List
	}
)

func main() {
	flag.Parse()

	gosnmp.Default.Target = *host
	gosnmp.Default.Community = *community
	gosnmp.Default.Timeout = *timeout

	// get a db connection, sqlite3 for now because I'm lazy
	db, err := sql.Open("sqlite3", *dbFile)
	if err != nil {
		log.WithFields(log.Fields{
			"dbFile": *dbFile,
			"err":    err,
		}).Fatal("Couldn't open db file!")
	}
	defer db.Close()

	// create table if it doesn't exist already
	sqlCreate := `
		CREATE TABLE IF NOT EXISTS logs (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			time DATE DEFAULT (datetime('now','utc')),
			apmac TEXT,
			apname TEXT,
			clientip TEXT,
			clientmac TEXT,
			clientssid TEXT,
			clientuser TEXT
		);
	`
	if _, err := db.Exec(sqlCreate); err != nil {
		log.WithFields(log.Fields{
			"dbFile": *dbFile,
			"err":    err,
		}).Fatal("Couldn't create table in sqlite3 db!")
	}

	dbStmt, err := db.Prepare("INSERT INTO logs(apmac, apname, clientip, clientmac, clientssid, clientuser) VALUES (?,?,?,?,?,?)")
	if err != nil {
		log.WithFields(log.Fields{
			"dbFile": *dbFile,
			"err":    err,
		}).Fatal("Couldn't prepare sql statement!")
	}

	if err := gosnmp.Default.Connect(); err != nil {
		log.WithFields(log.Fields{
			"host":      *host,
			"community": *community,
			"timeout":   *timeout,
			"err":       err,
		}).Fatal("Couldn't open SNMP socket!")
	}
	defer gosnmp.Default.Conn.Close()

	// run every interval, regardless of whether there is an outstanding request or not
	ticker := time.NewTicker(*pollInterval)
	defer ticker.Stop()
	var iteration int
	for tick := range ticker.C {
		// track how many of these things we've done
		// this is primarily useful in determining if the SNMP timeout/interval is wrong
		iteration++
		log.WithFields(log.Fields{
			"iteration": iteration,
		}).Info("Starting new collection job")

		// don't block
		go func(tick time.Time, iteration int, dbStmt *sql.Stmt) {
			// start counting for time statistics
			timeStart := time.Now()

			// get the data from the SNMP Target
			var results []gosnmp.SnmpPDU
			for _, oid := range oids {
				result, err := gosnmp.Default.BulkWalkAll(oid)
				if err != nil {
					log.WithFields(log.Fields{
						"oid": oid,
						"err": err,
					}).Warn("Couldn't walk SNMP!")
				}
				results = append(results, result...)
			}
			// how long did the SNMP querying take?
			log.WithFields(log.Fields{
				"Iteration": iteration,
				"Duration":  timeStart.Sub(time.Now()),
			}).Info("SNMP Collection Completed")

			// reset the time to now monitor how long the DB work took
			timeStart = time.Now()

			// parse the SNMP results, sort them into client uuid buckets
			clients := make(map[string]*struct{ apMAC, apName, clientIP, clientMAC, clientSSID, clientUser string })
			for _, result := range results {
				switch {
				case strings.HasPrefix(result.Name, oids[0]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.4" // AP MAC List
					uuid := strings.TrimPrefix(result.Name, oids[0])
					if result.Type == gosnmp.OctetString {
						clients[uuid].apMAC = string(result.Value.([]byte))
					}
				case strings.HasPrefix(result.Name, oids[1]):
					// ".1.3.6.1.4.1.14179.2.2.1.1.3" // AP Names
					uuid := strings.TrimPrefix(result.Name, oids[1])
					if result.Type == gosnmp.OctetString {
						clients[uuid].apName = string(result.Value.([]byte))
					}
				case strings.HasPrefix(result.Name, oids[2]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.2" // Client IP List
					uuid := strings.TrimPrefix(result.Name, oids[2])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientIP = string(result.Value.([]byte))
					}
				case strings.HasPrefix(result.Name, oids[3]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.1" // Client MAC List
					uuid := strings.TrimPrefix(result.Name, oids[3])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientMAC = string(result.Value.([]byte))
					}
				case strings.HasPrefix(result.Name, oids[4]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.7" // Client SSID List
					uuid := strings.TrimPrefix(result.Name, oids[4])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientSSID = string(result.Value.([]byte))
					}
				case strings.HasPrefix(result.Name, oids[5]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.3" // Client Username List
					uuid := strings.TrimPrefix(result.Name, oids[5])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientUser = string(result.Value.([]byte))
					}
				}
			}

			// now get all the stored clients and put them in the database
			for _, data := range clients {
				if _, err := dbStmt.Exec(data.apMAC, data.apName, data.clientIP, data.clientMAC, data.clientSSID, data.clientUser); err != nil {
					log.WithFields(log.Fields{
						"dbField": *dbFile,
						"err":     err,
					}).Warn("WARNING: sql insert failed")
					return
				}
			}
			// how long did the DB work take?
			log.WithFields(log.Fields{
				"Iteration": iteration,
				"Duration":  timeStart.Sub(time.Now()),
			}).Info("Database Inserts Completed")

		}(tick, iteration, dbStmt)
	}

	// if we've got here, somehow the ticker has broken.
	log.Fatal("This should never be reached!")

}
