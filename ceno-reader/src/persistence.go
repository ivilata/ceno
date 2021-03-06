/**
 * Functionality to persist information about feeds that
 * have been subscribed to and any data we might want to
 * keep about things like the number of items retrieved.
 */

package main

import (
	"database/sql"
	"fmt"
	rss "github.com/jteeuwen/go-pkg-rss"
	_ "github.com/mattn/go-sqlite3"
	"time"
)

// SQLite table initialization statements

// Create the table that contains information about RSS/Atom feeds the reader follows
const createFeedsTable = `create table if not exists feeds(
    id integer primary key,
    url varchar(255) unique on conflict ignore,
    type varchar(8),
    charset varchar(64),
    articles integer,
    lastPublished varchar(64),
    latest varchar(255),
	title varchar(255),
	direction varchar(64)
);`

// Create the table that contains information about items received from feeds
const createItemsTable = `create table if not exists items(
    id integer primary key,
    title varchar(255),
    url varchar(255),
    feed_url varchar(255),
    authors text,
    published varchar(64)
);`

// Create a table containing information about errors encountered trying to subscribe
// to feeds, handling articles, etc.
// These errors can be retrieved as a report later that can be used to diagnose problems
// with specific feeds
const createErrorsTable = `create table if not exists errors(
	id integer primary key,
	resource_types integer,
	error_types integer,
	message text
);`

// A list of the tables to try to initialize when the reader starts
var tableInitializers = []string{
	createFeedsTable,
	createItemsTable,
	createErrorsTable,
}

/**
 * Repeatedly test a condition until it passes, allowing a thread to block
 * on the condition.
 * @param {func() bool} condition - A closure that will test the condition
 * @param {time.Duration} testRate - The frequency at which to invoke the condition function
 * @return A channel from which the number of calls to the condition were made when it passes
 */
func WaitUntilPass(condition func() bool, testRate time.Duration) chan int {
	reportAttempts := make(chan int, 1)
	go func() {
		attempts := 0
		passed := false
		for !passed {
			attempts++
			passed = condition()
			<-time.After(testRate)
		}
		reportAttempts <- attempts
	}()
	return reportAttempts
}

/**
 * Create a database connection to SQLite3 and initialize (if not already)
 * the tables used to store information about RSS feeds being followed etc.
 * @param {string} dbFileName - The name of the file to keep the database info in
 */
func InitDBConnection(dbFileName string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbFileName)
	if err != nil {
		return nil, err
	}
	for _, initializer := range tableInitializers {
		_, err = db.Exec(initializer)
		if err != nil {
			break
		}
	}
	return db, err
}

/**
 * Persist information about a feed to the storage medium.
 * @param {*sql.DB} db - The database connection to use
 * @param {Feed} feed - Information describing the new feed to save
 * @return any error that occurs trying to run the query
 */
func SaveFeed(db *sql.DB, feed Feed) error {
	tx, err1 := db.Begin()
	if err1 != nil {
		return err1
	}
	_, err2 := tx.Exec(`
        insert into feeds(url, type, charset, articles, lastPublished, latest, title, direction)
        values(?,?,?,?,?,?,?,?)`,
		feed.Url, feed.Type, feed.Charset, feed.Articles, feed.LastPublished, feed.Latest, feed.Title, feed.Direction)
	if err2 != nil {
		return err2
	}
	tx.Commit()
	return nil
}

/**
 * Get a collection of all the feeds subscribed to.
 * @param {*sql.DB} db - The database connection to use
 * @return the collection of feeds retrieved from the database and any error that occurs
 */
func AllFeeds(db *sql.DB) ([]Feed, error) {
	var feeds []Feed
	rows, err2 := db.Query(`select id, url, type, charset, articles, lastPublished, latest, title, direction
                            from feeds`)
	if err2 != nil {
		return feeds, err2
	}
	for rows.Next() {
		var url, _type, charset, lastPublished, latest, title, direction string
		var id, articles int
		rows.Scan(&id, &url, &_type, &charset, &articles, &lastPublished, &latest, &title, &direction)
		fmt.Printf("Found feed %s with %d articles. Last published %s on %s.\n",
			url, articles, latest, lastPublished)
		feeds = append(feeds, Feed{id, url, _type, charset, articles, lastPublished, latest, title, direction})
	}
	rows.Close()
	return feeds, nil
}

/**
 * Get the basic information about a persisted feed from its URL.
 * @param {*sql.DB} db - The database connection to use
 * @param {string} url - The URL to search for
 * @return the feed retrieved from the database and any error that occurs
 */
func GetFeed(db *sql.DB, url string) (Feed, error) {
	var feed Feed
	var id, articles int
	var _type, charset, lastPublished, latest, title, direction string
	log("reading feed info from database")
	err2 := db.QueryRow(`select id, url, type, charset, articles, lastPublished, latest, title, direction from feeds where url=?`, url).Scan(&id, &url, &_type, &charset, &articles, &lastPublished, &latest, &title, &direction)
	if err2 != nil {
		log("Failed to retrieve feed info from the db")
		return feed, err2
	}
	log("feed info: " + url + " " + title + " " + direction)
	return Feed{id, url, _type, charset, articles, lastPublished, latest, title, direction}, nil
}

/**
 * Delete a feed by referencing its URL.
 * @param {*sql.DB} db - The database connection to use
 * @param {string} url - The URL of the feed
 * @return any error that occurs executing the delete statement
 */
func DeleteFeed(db *sql.DB, url string) error {
	tx, err1 := db.Begin()
	if err1 != nil {
		return err1
	}
	_, err2 := tx.Exec("delete from feeds where url=?", url)
	if err2 != nil {
		return err2
	}
	tx.Commit()
	return nil
}

/**
 * Store information about an item in a channel from a particular feed
 * @param {*sql.DB} db - the database connection to use
 * @param {string} feedUrl - The URL of the RSS/Atom feed
 * @param {*rss.Item} item - The item to store the content of
 * @return any error that occurs saving the item
 */
func SaveItem(db *sql.DB, feedUrl string, item *rss.Item) error {
	tx, err1 := db.Begin()
	if err1 != nil {
		return err1
	}
	url := item.Links[0].Href
	authors := item.Author.Name
	if item.Contributors != nil {
		for _, contrib := range item.Contributors {
			authors += " " + contrib
		}
	}
	// Insert the item itself
	_, err2 := tx.Exec(`
        insert into items(title, url, feed_url, authors, published)
        values(?, ?, ?, ?, ?)`,
		item.Title, url, feedUrl, authors, item.PubDate)
	if err2 != nil {
		return err2
	}
	_, err3 := tx.Exec(`
        update feeds
        set articles=articles+1, lastPublished=?, latest=?
        where url=?`,
		item.PubDate, item.Title, feedUrl)
	if err3 != nil {
		return err3
	}
	tx.Commit()
	return nil
}

/**
 * Get the items stored for a particular feed in reference to its URL.
 * @param {*sql.DB} db - the database connection to use
 * @param {string} url - The URL of the feed to get items from
 * @return the collection of items retrieved from the database and any error that occurs
 */
func GetItems(db *sql.DB, feedUrl string) ([]Item, error) {
	var items []Item
	rows, err2 := db.Query(`select id, title, url, authors, published from items where feed_url=?`,
		feedUrl)
	if err2 != nil {
		return items, err2
	}
	for rows.Next() {
		var id int
		var title, authors, published, url string
		rows.Scan(&id, &title, &url, &authors, &published)
		items = append(items, Item{id, title, url, feedUrl, authors, published})
	}
	rows.Close()
	return items, nil
}

/**
 * Delete a particular item.
 * @param {*sql.DB} db - The database connection to use
 * @param {int} id - The identifier of the item to delete
 * @return any error that occurs running the delete statement
 */
func DeleteItem(db *sql.DB, id int) error {
	tx, err1 := db.Begin()
	if err1 != nil {
		return err1
	}
	_, err2 := tx.Exec("delete from items where id=?")
	if err2 != nil {
		return err2
	}
	tx.Commit()
	return nil
}

/**
 * Save a report of the incidence of an error having occurred.
 * Note that converting between encoded resource types and error classifications
 * occurs strictly in the CENO Reader codebase. We aren't going to bother doing it in SQL.
 * @param {*sql.DB} db - The database connection to use
 * @param {Errorreport} report - Information about the error that occurred
 * @return any error that occurs saving the error report information
 */
func SaveError(db *sql.DB, report ErrorReport) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	_, execErr := tx.Exec(`
		insert into errors (resource_types, error_types, message) values(?, ?, ?)`,
		report.ResourceTypes, report.ErrorTypes, report.ErrorMessage)
	if execErr != nil {
		return execErr
	}
	tx.Commit()
	return nil
}

/**
 * Get an array of ErrorReports corresponding to the kinds specified by the
 * argument to this function.  Once error reports are retrieved from the database,
 * they are deleted since we want to avoid reporting errors twice.
 * @param {*sql.DB} db - The database connection to use
 * @return the collection of error reports retrieved from the database and any error that occurs
 */
func GetErrors(db *sql.DB) ([]ErrorReport, error) {
	reports := make([]ErrorReport, 0)
	// Get the relevant rows from the database.
	// Note that error types and resource types are specified in the database as integers.
	// That means we do the usual binary operations to find them.
	rows, queryError := db.Query(`select id, resource_types, error_types, message from errors`)
	if queryError != nil {
		return reports, queryError
	}
	for rows.Next() {
		var id, resourceTypes, errorTypes int
		var message string
		rows.Scan(&id, &resourceTypes, &errorTypes, &message)
		reports = append(reports, ErrorReport{
			id, Resource(resourceTypes), ErrorClass(errorTypes), message,
		})
	}
	rows.Close()
	_, execError := db.Exec(`delete from errors`)
	if execError != nil {
		return reports, execError
	}
	return reports, nil
}
