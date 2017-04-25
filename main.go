package main

import (
	"database/sql"
	"encoding/hex"
	"net"

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
		".1.3.6.1.4.1.14179.2.1.4.1.4",  // AP MAC List
		".1.3.6.1.4.1.14179.2.2.1.1.3",  // AP Names
		".1.3.6.1.4.1.14179.2.2.2.1.4",  // AP Channel
		".1.3.6.1.4.1.14179.2.1.4.1.2",  // Client IP List
		".1.3.6.1.4.1.14179.2.1.4.1.1",  // Client MAC List
		".1.3.6.1.4.1.14179.2.1.4.1.7",  // Client SSID List
		".1.3.6.1.4.1.14179.2.1.4.1.3",  // Client Username List
		".1.3.6.1.4.1.14179.2.1.4.1.25", // Client Protocol (a/b/g/n etc)
		".1.3.6.1.4.1.14179.2.1.6.1.1",  // Client RSSI
		".1.3.6.1.4.1.14179.2.1.6.1.26", // Client SNR
		".1.3.6.1.4.1.14179.2.1.6.1.2",  // Client Bytes Recv
		".1.3.6.1.4.1.14179.2.1.6.1.3",  // Client Bytes Sent
	}
)

type client struct {
	apMAC           string
	apName          string
	apChannel       int
	clientIP        string
	clientMAC       string
	clientSSID      string
	clientUser      string
	clientProto     int
	clientRSSI      int
	clientSNR       int
	clientBytesRecv int
	clientBytesSent int
}

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
	defer func(db *sql.DB) {
		if err := db.Close(); err != nil {
			log.WithFields(log.Fields{
				"dbFile": *dbFile,
				"err":    err,
			}).Fatal("Couldn't close db file!")
		}
	}(db)

	// create table if it doesn't exist already
	sqlCreate := `
		CREATE TABLE IF NOT EXISTS logs (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			time DATE DEFAULT (datetime('now','utc')),
			apmac TEXT,
			apname TEXT,
			apchannel TEXT,
			clientip TEXT,
			clientmac TEXT,
			clientssid TEXT,
			clientuser TEXT,
			clientproto TEXT,
			clientrssi TEXT,
			clientsnr TEXT,
			clientrecv TEXT,
			clientsent TEXT
		);
	`
	if _, err := db.Exec(sqlCreate); err != nil {
		log.WithFields(log.Fields{
			"dbFile": *dbFile,
			"err":    err,
		}).Fatal("Couldn't create table in sqlite3 db!")
	}

	dbStmt, err := db.Prepare("INSERT INTO logs(apmac, apname, apchannel, clientip, clientmac, clientssid, clientuser, clientproto, clientrssi, clientsnr, clientrecv, clientsent) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)")
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
	defer func() {
		if err := gosnmp.Default.Conn.Close(); err != nil {
			log.WithFields(log.Fields{
				"host":      *host,
				"community": *community,
				"timeout":   *timeout,
				"err":       err,
			}).Fatal("Couldn't close SNMP socket!")
		}
	}()

	// run every interval, regardless of whether there is an outstanding request or not
	ticker := time.NewTicker(*pollInterval)
	defer ticker.Stop()
	var iteration int
	for tick := range ticker.C {
		// track how many of these things we've done
		// this is primarily useful in determining if the SNMP timeout/interval is wrong
		iteration++
		log.WithFields(log.Fields{
			"Iteration": iteration,
		}).Info("Starting new collection job")

		// don't block
		go func(tick time.Time, iteration int, dbStmt *sql.Stmt) {
			// start counting for time statistics
			timeStart := time.Now()
			iterationLogger := log.WithFields(log.Fields{
				"Iteration": iteration,
			})

			// get the data from the SNMP Target
			var results []gosnmp.SnmpPDU
			for _, oid := range oids {
				timeWalk := time.Now()
				result, err := gosnmp.Default.BulkWalkAll(oid)
				if err != nil {
					log.WithFields(log.Fields{
						"Iteration": iteration,
						"oid":       oid,
						"err":       err,
						"duration":  time.Now().Sub(timeWalk),
					}).Error("Walking SNMP did not come back cleanly!")
				}
				results = append(results, result...)
			}
			// how long did the SNMP querying take?
			iterationLogger.WithFields(log.Fields{
				"duration": time.Now().Sub(timeStart),
			}).Info("SNMP Collection Completed")

			// reset the time to now monitor how long the DB work took
			timeStart = time.Now()

			// parse the SNMP results, sort them into client uuid buckets
			clients := make(map[string]*client)
			for _, result := range results {
				switch {
				case strings.HasPrefix(result.Name, oids[0]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.4" // AP MAC List
					/*
						bsnMobileStationAPMacAddr OBJECT-TYPE
						    SYNTAX MacAddress
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "802.11 Mac Address of the AP to which the
						        Mobile Station is associated."
						    ::= { bsnMobileStationEntry 4 }

						MacAddress ::= TEXTUAL-CONVENTION
						    DISPLAY-HINT "1x:"
						    STATUS       current
						    DESCRIPTION
						            "Represents an 802 MAC address represented in the
						            `canonical' order defined by IEEE 802.1a, i.e., as if it
						            were transmitted least significant bit first, even though
						            802.5 (in contrast to other 802.x protocols) requires MAC
						            addresses to be transmitted most significant bit first."
						    SYNTAX       OCTET STRING (SIZE (6))
					*/
					uuid := strings.TrimPrefix(result.Name, oids[0])
					if result.Type == gosnmp.OctetString {
						clients[uuid].apMAC = hex.EncodeToString(result.Value.([]byte))
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[1]):
					// ".1.3.6.1.4.1.14179.2.2.1.1.3" // AP Names
					/*
						bsnAPName OBJECT-TYPE
						    SYNTAX OCTET STRING(SIZE(0..32))
						    ACCESS read-write
						    STATUS mandatory
						    DESCRIPTION
						        "Name assigned to this AP. If an AP is not configured its
						        factory default name will be ap: eg. ap:af:12:be"
						    ::= { bsnAPEntry 3 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[1])
					if result.Type == gosnmp.OctetString {
						clients[uuid].apName = string(result.Value.([]byte))
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[2]):
					// ".1.3.6.1.4.1.14179.2.2.2.1.4" // AP Channel
					/*
						Current channel number of the AP Interface.
						Channel numbers will be from 1 to 14 for 802.11b interface type.
						Channel numbers will be from 34 to 169 for 802.11a interface
						type. Allowed channel numbers also depends on the current
						Country Code set in the Switch. This attribute cannot be set
						unless bsnAPIfPhyChannelAssignment is set to customized else
						this attribute gets assigned by dynamic algorithm.

						bsnAPIfPhyChannelNumber OBJECT-TYPE
						    SYNTAX INTEGER {
						        ch1(1),
						        ch2(2),
						        ch3(3),
						        ch4(4),
						        ch5(5),
						        ch6(6),
						        ch7(7),
						        ch8(8),
						        ch9(9),
						        ch10(10),
						        ch11(11),
						        ch12(12),
						        ch13(13),
						        ch14(14),
						        ch20(20),
						        ch21(21),
						        ch22(22),
						        ch23(23),
						        ch24(24),
						        ch25(25),
						        ch26(26),
						        ch34(34),
						        ch36(36),
						        ch38(38),
						        ch40(40),
						        ch42(42),
						        ch44(44),
						        ch46(46),
						        ch48(48),
						        ch52(52),
						        ch56(56),
						        ch60(60),
						        ch64(64),
						        ch100(100),
						        ch104(104),
						        ch108(108),
						        ch112(112),
						        ch116(116),
						        ch120(120),
						        ch124(124),
						        ch128(128),
						        ch132(132),
						        ch136(136),
						        ch140(140),
						        ch149(149),
						        ch153(153),
						        ch157(157),
						        ch161(161),
						        ch165(165),
						        ch169(169)
						        }
						    ACCESS read-write
						    STATUS mandatory
						    DESCRIPTION
						        "Current channel number of the AP Interface.
						        Channel numbers will be from 1 to 14 for 802.11b interface type.
						        Channel numbers will be from 34 to 169 for 802.11a interface
						        type.  Allowed channel numbers also depends on the current
						        Country Code set in the Switch. This attribute cannot be set
						        unless bsnAPIfPhyChannelAssignment is set to customized else
						        this attribute gets assigned by dynamic algorithm."
						    ::= { bsnAPIfEntry 4 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[2])
					if result.Type == gosnmp.Integer {
						clients[uuid].apChannel = int(gosnmp.ToBigInt(result.Value).Int64())
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[3]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.2" // Client IP List
					/*
						bsnMobileStationIpAddress OBJECT-TYPE
						    SYNTAX IpAddress
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "IP Address of the Mobile Station"
						    ::= { bsnMobileStationEntry 2 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[3])
					if result.Type == gosnmp.OctetString || result.Type == gosnmp.IPAddress {
						clients[uuid].clientIP = net.IP(result.Value.([]byte)).String()
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[4]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.1" // Client MAC List
					/*
						bsnMobileStationMacAddress OBJECT-TYPE
						    SYNTAX MacAddress

						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "802.11 MAC Address of the Mobile Station."
						    ::= { bsnMobileStationEntry 1 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[4])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientMAC = hex.EncodeToString(result.Value.([]byte))
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[5]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.7" // Client SSID List
					/*
						bsnMobileStationSsid OBJECT-TYPE
						    SYNTAX DisplayString

						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "The SSID Advertised by Mobile Station"
						    ::= { bsnMobileStationEntry 7 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[5])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientSSID = string(result.Value.([]byte))
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[6]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.3" // Client Username List
					/*
						bsnMobileStationUserName OBJECT-TYPE
						    SYNTAX DisplayString

						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "User Name,if any, of the Mobile Station. This would
						        be non empty in case of Web Authentication and IPSec."
						    ::= { bsnMobileStationEntry 3 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[6])
					if result.Type == gosnmp.OctetString {
						clients[uuid].clientUser = string(result.Value.([]byte))
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[7]):
					// ".1.3.6.1.4.1.14179.2.1.4.1.25" // Client Protocol (a/b/g/n etc)
					/*
						bsnMobileStationProtocol OBJECT-TYPE
						    SYNTAX INTEGER {
						        dot11a(1),
						        dot11b(2),
						        dot11g(3),
						        unknown(4),
						        mobile(5),
						        dot11n24(6),
						        dot11n5(7)
						        }
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "The 802.11 protocol type of the client. The protocol
						        is mobile when this client detail is seen on the
						        anchor i.e it's mobility status is anchor."
						    ::= { bsnMobileStationEntry 25 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[7])
					if result.Type == gosnmp.Integer {
						clients[uuid].clientProto = int(gosnmp.ToBigInt(result.Value).Int64())
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[8]):
					// ".1.3.6.1.4.1.14179.2.1.6.1.1" // Client RSSI
					/*
						bsnMobileStationRSSI OBJECT-TYPE
						    SYNTAX INTEGER
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "Average packet RSSI for the Mobile Station."
						    ::= { bsnMobileStationStatsEntry 1 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[8])
					if result.Type == gosnmp.Integer {
						clients[uuid].clientRSSI = int(gosnmp.ToBigInt(result.Value).Int64())
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[9]):
					// ".1.3.6.1.4.1.14179.2.1.6.1.26" // Client SNR
					/*
						bsnMobileStationSnr OBJECT-TYPE
						    SYNTAX INTEGER
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "Signal to noise Ratio of the Mobile Station."
						    ::= { bsnMobileStationStatsEntry 26 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[9])
					if result.Type == gosnmp.Integer {
						clients[uuid].clientSNR = int(gosnmp.ToBigInt(result.Value).Int64())
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[10]):
					// ".1.3.6.1.4.1.14179.2.1.6.1.2",  // Client Bytes Recv
					/*
						bsnMobileStationBytesReceived OBJECT-TYPE
						    SYNTAX
						           Counter
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "Bytes received from Mobile Station"
						    ::= { bsnMobileStationStatsEntry 2 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[10])
					if result.Type == gosnmp.Counter32 ||
						result.Type == gosnmp.Counter64 ||
						result.Type == gosnmp.Integer {
						clients[uuid].clientBytesRecv = int(gosnmp.ToBigInt(result.Value).Int64())
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				case strings.HasPrefix(result.Name, oids[11]):
					// ".1.3.6.1.4.1.14179.2.1.6.1.3",  // Client Bytes Sent
					/*
						bsnMobileStationBytesSent OBJECT-TYPE
						    SYNTAX
						           Counter
						    ACCESS read-only
						    STATUS mandatory
						    DESCRIPTION
						        "Bytes sent to Mobile Station"
						    ::= { bsnMobileStationStatsEntry 3 }
					*/
					uuid := strings.TrimPrefix(result.Name, oids[11])
					if result.Type == gosnmp.Counter32 ||
						result.Type == gosnmp.Counter64 ||
						result.Type == gosnmp.Integer {
						clients[uuid].clientBytesSent = int(gosnmp.ToBigInt(result.Value).Int64())
					} else {
						iterationLogger.WithFields(log.Fields{
							"Type": result.Type,
						}).Warn("Bad/Unexpected SNMP Data")
					}
				}
			}

			// now get all the stored clients and put them in the database
			for _, data := range clients {
				if _, err := dbStmt.Exec(
					data.apMAC,
					data.apName,
					data.apChannel,
					data.clientIP,
					data.clientMAC,
					data.clientSSID,
					data.clientUser,
					data.clientProto,
					data.clientRSSI,
					data.clientSNR,
					data.clientBytesRecv,
					data.clientBytesSent,
				); err != nil {
					iterationLogger.WithFields(log.Fields{
						"dbField": *dbFile,
						"err":     err,
					}).Warn("WARNING: sql insert failed")
					return
				}
			}
			// how long did the DB work take?
			iterationLogger.WithFields(log.Fields{
				"duration": time.Now().Sub(timeStart),
			}).Info("Database Inserts Completed")

		}(tick, iteration, dbStmt)
	}

	// if we've got here, somehow the ticker has broken.
	log.Fatal("This should never be reached!")

}
