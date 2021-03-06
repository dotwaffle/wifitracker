package main

import (
	"database/sql"
	"fmt"
	"net"

	log "github.com/Sirupsen/logrus"
	_ "github.com/go-sql-driver/mysql"

	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/namsral/flag"
	"github.com/soniah/gosnmp"
)

var (
	configFile       = flag.String(flag.DefaultConfigFlagname, "", "Path to Configuration File (optional)")
	snmpCommunity    = flag.String("snmpcommunity", "public", "SNMP community string")
	snmpHost         = flag.String("snmphost", "localhost", "SNMP host to query")
	snmpPollInterval = flag.Duration("snmppollinterval", 10*time.Second, "SNMP Polling interval")
	snmpRetries      = flag.Int("snmpretries", 1, "SNMP retries")
	snmpTimeout      = flag.Duration("snmptimeout", 1*time.Second, "SNMP timeout")
	sqlHost          = flag.String("sqlhost", "localhost", "MySQL Host")
	sqlPort          = flag.Int("sqlport", 3306, "MySQL Port")
	sqlUser          = flag.String("sqluser", "user", "MySQL User")
	sqlPass          = flag.String("sqlpass", "pass", "MySQL Pass")
	sqlDB            = flag.String("sqldb", "wifi", "MySQL Database")
	sqlTLS           = flag.String("sqltls", "false", "MySQL TLS (default \"false\") (true, false, skip-verify)")
	debug            = flag.Bool("debug", false, "Turn on debugging output")
	oids             = [...]string{
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

type ap struct {
	apMAC          string
	apName         string
	apChannel24GHz int // 2.4GHz, obviously
	apChannel5GHz  int
}

func main() {
	flag.Parse()

	gosnmp.Default.Target = *snmpHost
	gosnmp.Default.Community = *snmpCommunity
	gosnmp.Default.Timeout = *snmpTimeout
	gosnmp.Default.Retries = *snmpRetries

	if *debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	// get a db connection
	log.Debug("Database Setup")
	dbDSN := fmt.Sprintf("%s:%s@tcp(%s)/%s?tls=%s",
		*sqlUser,
		*sqlPass,
		net.JoinHostPort(*sqlHost, strconv.Itoa(*sqlPort)),
		*sqlDB,
		*sqlTLS)
	db, err := sql.Open("mysql", dbDSN)
	if err != nil {
		log.WithFields(log.Fields{
			"dsn": dbDSN,
			"err": err,
		}).Fatal("Couldn't open db file!")
	}
	defer func(db *sql.DB) {
		if err := db.Close(); err != nil {
			log.WithFields(log.Fields{
				"dsn": dbDSN,
				"err": err,
			}).Fatal("Couldn't close db connection!")
		}
	}(db)

	log.Debug("Database Ping")
	if err := db.Ping(); err != nil {
		log.WithFields(log.Fields{
			"dsn": dbDSN,
			"err": err,
		}).Fatal("Couldn't ping db!")
	}

	// create table if it doesn't exist already
	log.Debug("Database Creation (if needed)")
	sqlCreateClients := `
		CREATE TABLE IF NOT EXISTS clients (
			id INTEGER NOT NULL PRIMARY KEY AUTO_INCREMENT,
			timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			apmac TEXT,
			clientip TEXT,
			clientmac TEXT,
			clientssid TEXT,
			clientuser TEXT,
			clientproto INTEGER,
			clientrssi INTEGER,
			clientsnr INTEGER,
			clientrecv INTEGER,
			clientsent INTEGER
		);
	`
	if _, err := db.Exec(sqlCreateClients); err != nil {
		log.WithFields(log.Fields{
			"table": "clients",
			"err":   err,
		}).Fatal("Couldn't create table in db!")
	}
	sqlCreateAPs := `
		CREATE TABLE IF NOT EXISTS aps (
			id INTEGER NOT NULL PRIMARY KEY AUTO_INCREMENT,
			timestamp TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			apmac TEXT,
			apname TEXT,
			apchannel24 INTEGER,
			apchannel5 INTEGER
		);
	`
	if _, err := db.Exec(sqlCreateAPs); err != nil {
		log.WithFields(log.Fields{
			"table": "aps",
			"err":   err,
		}).Fatal("Couldn't create table in db!")
	}

	log.Debug("Database Prepared Statement Loading")
	dbStmtClient, err := db.Prepare("INSERT INTO clients(timestamp, apmac, clientip, clientmac, clientssid, clientuser, clientproto, clientrssi, clientsnr, clientrecv, clientsent) VALUES (?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		log.WithFields(log.Fields{
			"err":   err,
			"table": "clients",
		}).Fatal("Couldn't prepare sql statement!")
	}
	dbStmtAP, err := db.Prepare("INSERT INTO aps(timestamp, apmac, apname, apchannel24, apchannel5) VALUES (?,?,?,?,?)")
	if err != nil {
		log.WithFields(log.Fields{
			"err":   err,
			"table": "aps",
		}).Fatal("Couldn't prepare sql statement!")
	}

	log.Debug("SNMP Connection Setup")
	if err := gosnmp.Default.Connect(); err != nil {
		log.WithFields(log.Fields{
			"host":      *snmpHost,
			"community": *snmpCommunity,
			"timeout":   *snmpTimeout,
			"retries":   *snmpRetries,
			"err":       err,
		}).Fatal("Couldn't open SNMP socket!")
	}
	defer func() {
		if err := gosnmp.Default.Conn.Close(); err != nil {
			log.WithFields(log.Fields{
				"host":      *snmpHost,
				"community": *snmpCommunity,
				"timeout":   *snmpTimeout,
				"retries":   *snmpRetries,
				"err":       err,
			}).Fatal("Couldn't close SNMP socket!")
		}
	}()

	// run every interval, regardless of whether there is an outstanding request or not
	log.Info("Fully setup, starting main loop!")
	ticker := time.NewTicker(*snmpPollInterval)
	defer ticker.Stop()
	var iteration int
	for timeStartJob := range ticker.C {
		// track how many of these things we've done
		// this is primarily useful in determining if the SNMP timeout/interval is wrong
		iteration++
		log.WithFields(log.Fields{
			"Iteration": iteration,
		}).Debug("Starting new collection job")

		// block, no point in having multiple collections running at the same time

		// start counting for time statistics
		timeStartCollect := time.Now()
		iterationLogger := log.WithFields(log.Fields{
			"Iteration": iteration,
		})

		// get the data from the SNMP Target
		var results []gosnmp.SnmpPDU
		for _, oid := range oids {
			timeStartWalk := time.Now()
			result, err := gosnmp.Default.BulkWalkAll(oid)
			if err != nil {
				log.WithFields(log.Fields{
					"Iteration": iteration,
					"oid":       oid,
					"err":       err,
					"duration":  time.Since(timeStartWalk),
				}).Error("Walking SNMP did not come back cleanly!")
			}
			results = append(results, result...)
		}
		// how long did the SNMP querying take?
		iterationLogger.WithFields(log.Fields{
			"results":  len(results),
			"duration": time.Since(timeStartCollect),
		}).Debug("SNMP Collection Completed")

		// parse the SNMP results, sort them into client uuid buckets
		clients := make(map[string]*client)
		aps := make(map[string]*ap)
		for _, result := range results {
			switch {
			case strings.HasPrefix(result.Name, oids[0]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].apMAC = hex.EncodeToString(result.Value.([]byte))
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[1]+"."):
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

				// the uuid is currently dotted decimal, we need it in hex
				uuid := strings.TrimPrefix(result.Name, oids[1]+".")
				uuidSplit := strings.Split(uuid, ".")
				mac := make([]byte, 0)

				// for each octet string, oonvert it to decimal
				for _, runeOctet := range uuidSplit {
					intOctet, err := strconv.Atoi(string(runeOctet))
					if err != nil {
						log.Error("ASCII to Integer failure")
					}
					mac = append(mac, byte(intOctet))
				}

				// there are six bytes in a MAC address
				// skip any trailing index
				apMAC := hex.EncodeToString(mac[0:6])

				if result.Type == gosnmp.OctetString {
					if _, ok := aps[apMAC]; !ok {
						aps[apMAC] = &ap{}
					}
					aps[apMAC].apName = string(result.Value.([]byte))
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[2]+"."):
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

				// the uuid is currently dotted decimal, we need it in hex
				uuid := strings.TrimPrefix(result.Name, oids[2]+".")
				uuidSplit := strings.Split(uuid, ".")
				mac := make([]byte, 0)

				// for each octet string, oonvert it to decimal
				for _, runeOctet := range uuidSplit {
					intOctet, err := strconv.Atoi(string(runeOctet))
					if err != nil {
						log.Error("ASCII to Integer failure")
					}
					mac = append(mac, byte(intOctet))
				}

				// there are six bytes in a MAC address
				// skip any trailing index
				apMAC := hex.EncodeToString(mac[0:6])

				if result.Type == gosnmp.Integer {
					if _, ok := aps[apMAC]; !ok {
						aps[apMAC] = &ap{}
					}
					// all 2.4GHz channels are in the range 1-14
					if channel := result.Value.(int); channel < 15 {
						aps[apMAC].apChannel24GHz = channel
					} else {
						aps[apMAC].apChannel5GHz = channel
					}
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[3]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					// ipAddress comes out as a string
					clients[uuid].clientIP = result.Value.(string)
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[4]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientMAC = hex.EncodeToString(result.Value.([]byte))
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[5]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientSSID = string(result.Value.([]byte))
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[6]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientUser = string(result.Value.([]byte))
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[7]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientProto = result.Value.(int)
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[8]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientRSSI = result.Value.(int)
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[9]+"."):
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
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientSNR = result.Value.(int)
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[10]+"."):
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
					result.Type == gosnmp.Counter64 {
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientBytesRecv = int(gosnmp.ToBigInt(result.Value).Int64())
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			case strings.HasPrefix(result.Name, oids[11]+"."):
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
					result.Type == gosnmp.Counter64 {
					if _, ok := clients[uuid]; !ok {
						clients[uuid] = &client{}
					}
					clients[uuid].clientBytesSent = int(gosnmp.ToBigInt(result.Value).Int64())
				} else {
					iterationLogger.WithFields(log.Fields{
						"type": result.Type,
						"oid":  result.Name,
					}).Warn("Bad/Unexpected SNMP Data")
				}
			default:
				iterationLogger.WithFields(log.Fields{
					"type": result.Type,
					"oid":  result.Name,
				}).Warn("Unknown SNMP Data Found")
			}
		}

		// now get all the stored clients and put them in the database
		timeStartInsert := time.Now()

		// by creating a transaction, we actually buffer everything into one execution
		// this is by far not the best way to do it, but it's a quick performance hack
		dbTx, err := db.Begin()
		if err != nil {
			iterationLogger.WithFields(log.Fields{
				"err": err,
			}).Warn("sql insert failed")
			return
		}
		defer func() {
			err := dbTx.Rollback()
			if err != nil {
				if !strings.Contains(err.Error(), "sql: Transaction has already been committed or rolled back") {
					log.WithFields(log.Fields{
						"err": err,
					}).Fatal("Couldn't rollback database transaction!")
				}
			}
		}()

		// for debugging, count how many rows we insert
		var rows int

		// insert the client data
		for _, data := range clients {
			res, err := dbStmtClient.Exec(
				timeStartCollect.UTC(),
				data.apMAC,
				data.clientIP,
				data.clientMAC,
				data.clientSSID,
				data.clientUser,
				data.clientProto,
				data.clientRSSI,
				data.clientSNR,
				data.clientBytesRecv,
				data.clientBytesSent,
			)
			if err != nil {
				iterationLogger.WithFields(log.Fields{
					"err":   err,
					"table": "clients",
				}).Warn("sql insert failed")
				return
			}
			rowsClient, err := res.RowsAffected()
			if err != nil {
				iterationLogger.WithFields(log.Fields{
					"err":   err,
					"table": "clients",
				}).Warn("sql counting failed")
			} else {
				rows += int(rowsClient)
			}
		}

		// insert the ap data
		for apMAC, data := range aps {
			res, err := dbStmtAP.Exec(
				timeStartCollect.UTC(),
				apMAC,
				data.apName,
				data.apChannel24GHz,
				data.apChannel5GHz,
			)
			if err != nil {
				iterationLogger.WithFields(log.Fields{
					"err":   err,
					"table": "aps",
				}).Warn("sql insert failed")
				return
			}
			rowsAP, err := res.RowsAffected()
			if err != nil {
				iterationLogger.WithFields(log.Fields{
					"err":   err,
					"table": "aps",
				}).Warn("sql counting failed")
			} else {
				rows += int(rowsAP)
			}
		}

		// commit the transaction, writing everything out to the db
		if err := dbTx.Commit(); err != nil {
			iterationLogger.WithFields(log.Fields{
				"duration": time.Since(timeStartInsert),
			}).Debug("Database inserts failed")
		}

		// how long did the DB work take?
		iterationLogger.WithFields(log.Fields{
			"rows":     rows,
			"duration": time.Since(timeStartInsert),
		}).Debug("Database inserts completed")

		// how long did everything take?
		iterationLogger.WithFields(log.Fields{
			"duration": time.Since(timeStartJob),
		}).Info("Collection complete")
	}

	// if we've got here, somehow the ticker has broken.
	log.Fatal("This should never be reached!")

}
