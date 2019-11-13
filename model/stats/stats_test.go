package stats

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/apimodels"
	"github.com/evergreen-ci/evergreen/db"
	"github.com/evergreen-ci/evergreen/model/task"
	"github.com/evergreen-ci/evergreen/model/testresult"
	"github.com/evergreen-ci/evergreen/util"
	adb "github.com/mongodb/anser/db"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson"
	mgobson "gopkg.in/mgo.v2/bson"
)

var baseTime = time.Date(2018, 7, 15, 16, 45, 0, 0, time.UTC)
var baseHour = time.Date(2018, 7, 15, 16, 0, 0, 0, time.UTC)
var baseDay = time.Date(2018, 7, 15, 0, 0, 0, 0, time.UTC)
var jobTime = time.Date(1998, 7, 12, 20, 45, 0, 0, time.UTC)
var commit1 = baseTime
var commit2 = baseTime.Add(26 * time.Hour)
var finish1 = baseTime.Add(5 * 24 * time.Hour)
var finish2 = baseTime.Add(7 * 24 * time.Hour)

type statsSuite struct {
	suite.Suite
}

func TestStatsSuite(t *testing.T) {
	suite.Run(t, new(statsSuite))
}

func (s *statsSuite) SetupTest() {
	collectionsToClear := []string{
		hourlyTestStatsCollection,
		dailyTestStatsCollection,
		dailyStatsStatusCollection,
		DailyTaskStatsCollection,
		task.Collection,
		task.OldCollection,
		testresult.Collection,
	}

	for _, coll := range collectionsToClear {
		s.Nil(db.Clear(coll))
	}
}

func (s *statsSuite) TestStatsStatus() {
	require := s.Require()

	// Check that we get a default status when there is no doc in the database.
	status, err := GetStatsStatus("p1")
	require.NoError(err)
	require.NotNil(status)
	// The default value is rounded off to the day so use a delta of over one day to cover all cases.
	oneDayOneMinute := 24*time.Hour + time.Minute
	expected := time.Now().Add(-defaultBackFillPeriod)
	require.WithinDuration(expected, status.LastJobRun, oneDayOneMinute)
	require.WithinDuration(expected, status.ProcessedTasksUntil, oneDayOneMinute)

	// Check that we can update the status and read the new values.
	err = UpdateStatsStatus("p1", baseHour, baseDay, time.Hour)
	require.NoError(err)

	status, err = GetStatsStatus("p1")
	require.NoError(err)
	require.NotNil(status)
	require.Equal(baseHour.UTC(), status.LastJobRun.UTC())
	require.Equal(baseDay.UTC(), status.ProcessedTasksUntil.UTC())
}

func (s *statsSuite) TestGenerateHourlyTestStats() {
	rand.Seed(314159265)
	require := s.Require()

	// Insert task docs.
	s.initTasks()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Generate hourly stats for project p1 and an unknown task.
	err := GenerateHourlyTestStats(ctx, GenerateOptions{
		ProjectID: "p1",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"unknown_task"},
		Runtime:   jobTime})
	require.NoError(err)
	require.Equal(0, s.countHourlyTestDocs())

	// Generate hourly stats for project p1.
	err = GenerateHourlyTestStats(ctx, GenerateOptions{
		ProjectID: "p1",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"task1"},
		Runtime:   jobTime})
	require.NoError(err)
	require.Equal(5, s.countHourlyTestDocs())

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p1",
		Requester:    "r1",
		TestFile:     "test1.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 0, 2, 0.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p1",
		Requester:    "r1",
		TestFile:     "test2.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 1, 1, 120.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p1",
		Requester:    "r1",
		TestFile:     "test3.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 2, 0, 12.5, jobTime, true)

	// Generate hourly stats for project p2
	// Testing old tasks.
	err = GenerateHourlyTestStats(ctx, GenerateOptions{
		ProjectID: "p2",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"task1"},
		Runtime:   jobTime,
	})
	require.NoError(err)
	require.Equal(8, s.countHourlyTestDocs()) // 3 more tests combination were added to the collection

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p2",
		Requester:    "r1",
		TestFile:     "test1.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 0, 3, 0.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p2",
		Requester:    "r1",
		TestFile:     "test2.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 1, 2, 120.0, jobTime, true)

	// Generate hourly stats for project p3.
	// Testing display task / execution task.
	err = GenerateHourlyTestStats(ctx, GenerateOptions{
		ProjectID: "p3",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"task_exec_1"},
		Runtime:   jobTime,
	})
	require.NoError(err)
	require.Equal(10, s.countHourlyTestDocs()) // 2 more tests combination were added to the collection

        doc, err := GetHourlyTestDoc(DbTestStatsId{
		Project:      "p3",
		Requester:    "r1",
		TestFile:     "test1.js",
		TaskName:     "task_exec_1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        })
	require.NoError(err)
	require.Nil(doc)

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p3",
		Requester:    "r1",
		TestFile:     "test1.js",
		TaskName:     "task_display_1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 0, 1, 0.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
		Project:      "p3",
		Requester:    "r1",
		TestFile:     "test2.js",
		TaskName:     "task_display_1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 1, 0, 120.0, jobTime, true)

	// Generate hourly stats for project p5.
	// Testing tests with status 'skip'.
	err = GenerateHourlyTestStats(ctx, GenerateOptions{
		ProjectID: "p5",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"task1", "task2"},
		Runtime:   jobTime})
	require.NoError(err)
	require.Equal(12, s.countHourlyTestDocs()) // 2 more tests combination were added to the collection.

	// test1.js passed once and was skipped once.
        s.validateDbTestStats(DbTestStatsId{
		Project:      "p5",
		Requester:    "r1",
		TestFile:     "test1.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 1, 0, 60.0, jobTime, true)

        // test2.js failed once and was skipped once.
        s.validateDbTestStats(DbTestStatsId{
                Project:      "p5",
                Requester:    "r1",
                TestFile:     "test2.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 0, 1, 0.0, jobTime, true)
}

func (s *statsSuite) TestGenerateHourlyTestStatsMerge() {
        rand.Seed(314159265)
        require := s.Require()

        // Insert task docs.
        s.initTasks()

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        // Generate hourly stats for project p1 and an unknown task.
        err := GenerateHourlyTestStatsUsingMerge(ctx, GenerateOptions{
                ProjectID: "p1",
                Requester: "r1",
                Window:    baseHour,
                Tasks:     []string{"unknown_task"},
                Runtime:   jobTime})
	require.NoError(err)
        require.Equal(0, s.countHourlyTestDocs())

        // Generate hourly stats for project p1.
        err = GenerateHourlyTestStatsUsingMerge(ctx, GenerateOptions{
                ProjectID: "p1",
                Requester: "r1",
                Window:    baseHour,
                Tasks:     []string{"task1"},
                Runtime:   jobTime})
        require.NoError(err)
        require.Equal(5, s.countHourlyTestDocs())

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 0, 2, 0.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test2.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 1, 1, 120.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test3.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 2, 0, 12.5, jobTime, true)

        // Generate hourly stats for project p2
        // Testing old tasks.
        err = GenerateHourlyTestStatsUsingMerge(ctx, GenerateOptions{
                ProjectID: "p2",
                Requester: "r1",
                Window:    baseHour,
                Tasks:     []string{"task1"},
                Runtime:   jobTime})
	require.NoError(err)
        require.Equal(8, s.countHourlyTestDocs()) // 3 more tests combination were added to the collection

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p2",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 0, 3, 0.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p2",
		Requester:    "r1",
		TestFile:     "test2.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseHour,
        }, baseHour.UTC(), 1, 2, 120.0, jobTime, true)

        // Generate hourly stats for project p3.
        // Testing display task / execution task.
        err = GenerateHourlyTestStatsUsingMerge(ctx, GenerateOptions{
                ProjectID: "p3",
                Requester: "r1",
                Window:    baseHour,
                Tasks:     []string{"task_exec_1"},
                Runtime:   jobTime})
	require.NoError(err)
        require.Equal(10, s.countHourlyTestDocs()) // 2 more tests combination were added to the collection

        doc, err := GetHourlyTestDoc(DbTestStatsId{
                Project:      "p3",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task_exec_1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        })
	require.NoError(err)
        require.Nil(doc)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p3",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task_display_1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 0, 1, 0.0, jobTime, true)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p3",
                Requester:    "r1",
                TestFile:     "test2.js",
                TaskName:     "task_display_1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 1, 0, 120.0, jobTime, true)

        // Generate hourly stats for project p5.
        // Testing tests with status 'skip'.
        err = GenerateHourlyTestStatsUsingMerge(ctx, GenerateOptions{
                ProjectID: "p5",
                Requester: "r1",
                Window:    baseHour,
                Tasks:     []string{"task1", "task2"},
                Runtime:   jobTime})
        require.NoError(err)
        require.Equal(12, s.countHourlyTestDocs()) // 2 more tests combination were added to the collection.

        // test1.js passed once and was skipped once.
        s.validateDbTestStats(DbTestStatsId{
                Project:      "p5",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 1, 0, 60.0, jobTime, true)

        // test2.js failed once and was skipped once.
        s.validateDbTestStats(DbTestStatsId{
                Project:      "p5",
                Requester:    "r1",
                TestFile:     "test2.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseHour,
        }, baseHour.UTC(), 0, 1, 0.0, jobTime, true)
}

func (s *statsSuite) TestGenerateDailyTestStatsFromHourly() {
	rand.Seed(314159265)
	require := s.Require()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Insert hourly test stats docs.
	s.initHourly()
	// Generate daily test stats for unknown task.
	err := GenerateDailyTestStatsFromHourly(ctx, GenerateOptions{
		ProjectID: "p1",
		Requester: "r1",
		Window:    baseDay,
		Tasks:     []string{"unknown_task"},
		Runtime:   jobTime})
	require.NoError(err)
	require.Equal(0, s.countDailyTestDocs())

	// Generate daily test stats for exiting task
	err = GenerateDailyTestStatsFromHourly(ctx, GenerateOptions{ProjectID: "p1", Requester: "r1", Window: baseDay, Tasks: []string{"task1"}, Runtime: jobTime})
	require.NoError(err)
	require.Equal(1, s.countDailyTestDocs())

        // This test directly creates hourly test stats without the associated testresults. So
        // we can't check the value this way.
        s.validateDbTestStatsNoLast(DbTestStatsId{
		Project:      "p1",
		Requester:    "r1",
		TestFile:     "test1.js",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseDay,
        }, baseDay.UTC(), 30, 5, 4.0, jobTime, false)
}

func (s *statsSuite) validateDailyTestResults(ctx context.Context, dailyOptions GenerateOptions) {

        require := s.Require()

        // Generate daily test stats for exiting task
        require.Equal(5, s.countDailyTestDocs())

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseDay,
        }, baseDay.UTC(), 0, 3, 0.0, jobTime, false)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test1.js",
                TaskName:     "task1",
                BuildVariant: "v2",
                Distro:       "d1",
                Date:         baseDay,
        }, baseDay.UTC(), 0, 1, 0.0, jobTime, false)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test2.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseDay,
        }, baseDay.UTC(), 2, 1, 120.0, jobTime, false)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test2.js",
                TaskName:     "task1",
                BuildVariant: "v2",
                Distro:       "d1",
                Date:         baseDay,
        }, baseDay.UTC(), 0, 1, 0.0, jobTime, false)

        s.validateDbTestStats(DbTestStatsId{
                Project:      "p1",
                Requester:    "r1",
                TestFile:     "test3.js",
                TaskName:     "task1",
                BuildVariant: "v1",
                Distro:       "d1",
                Date:         baseDay,
        }, baseDay.UTC(), 2, 0, 12.5, jobTime, false)
}

func (s *statsSuite) TestGenerateDailyTestStatsChained() {
        // The merge version does not generate the Daily test stats from the hourly test stats.
        // This approach is simpler and allows the calculations to be run independently and to
        // be independently restarted.
        rand.Seed(314159265)
        require := s.Require()

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        // Insert tasks.
        s.initTasks()

        // Generate daily test stats for unknown task.
        err := GenerateDailyTestStatsFromHourly(ctx, GenerateOptions{
                ProjectID: "p1",
                Requester: "r1",
                Window:    baseDay,
                Tasks:     []string{"unknown_task"},
                Runtime:   jobTime})
        require.NoError(err)
        require.Equal(0, s.countDailyTestDocs())

        // Generate daily test stats for exiting task
        generateOptions := GenerateOptions{ProjectID: "p1", Requester: "r1", Window: baseDay, Tasks: []string{"task1"}, Runtime: jobTime}
        for hour := 0; hour < 24; hour++ {
                err = GenerateHourlyTestStats(ctx, GenerateOptions{
                        ProjectID: generateOptions.ProjectID,
                        Requester: generateOptions.Requester,
                        Window:    generateOptions.Window.Add(time.Hour * time.Duration(hour)),
                        Tasks:     generateOptions.Tasks,
                        Runtime:   generateOptions.Runtime})
                require.NoError(err)
	}

        // Generate daily test stats for exiting task
        err = GenerateDailyTestStatsFromHourly(ctx, generateOptions)
        require.NoError(err)

        s.validateDailyTestResults(ctx, generateOptions)
}

func (s *statsSuite) TestGenerateDailyTestStatsMerge() {
        // The merge version does not generate the Daily test stats from the hourly test stats.
        // This approach is simpler and allows the calculations to be run independently and to
        // be independently restarted.
        rand.Seed(314159265)
        require := s.Require()

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        // Insert hourly test stats docs.
        s.initTasks()

        // Generate daily test stats for unknown task.
        err := GenerateDailyTestStatsUsingMerge(ctx, GenerateOptions{
                ProjectID: "p1",
                Requester: "r1",
                Window:    baseDay,
                Tasks:     []string{"unknown_task"},
                Runtime:   jobTime})
        require.NoError(err)
        require.Equal(0, s.countDailyTestDocs())

        // Generate daily test stats for exiting task
        generateOptions := GenerateOptions{ProjectID: "p1", Requester: "r1", Window: baseDay, Tasks: []string{"task1"}, Runtime: jobTime}
        err = GenerateDailyTestStatsUsingMerge(ctx, generateOptions)
	require.NoError(err)

        s.validateDailyTestResults(ctx, generateOptions)
}

func (s *statsSuite) TestGenerateDailyTaskStats() {
	require := s.Require()

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        // Insert task docs.
        s.initTasks()

	// Generate task stats for project p1 and an unknown task.
	err := GenerateDailyTaskStats(ctx, GenerateOptions{
		ProjectID: "p1",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"unknown_task"},
		Runtime:   jobTime})
	require.NoError(err)
	require.Equal(0, s.countDailyTaskDocs())

	// Generate task stats for project p1.
	err = GenerateDailyTaskStats(ctx, GenerateOptions{
		ProjectID: "p1",
		Requester: "r1",
		Window:    baseHour,
		Tasks:     []string{"task1", "task2"},
		Runtime:   jobTime})
	require.NoError(err)
	require.Equal(3, s.countDailyTaskDocs())
	doc, err := GetDailyTaskDoc(DbTaskStatsId{
		Project:      "p1",
		Requester:    "r1",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseDay,
	})
	require.NoError(err)
	require.NotNil(doc)
	doc, err = GetDailyTaskDoc(DbTaskStatsId{
		Project:      "p1",
		Requester:    "r1",
		TaskName:     "task1",
		BuildVariant: "v2",
		Distro:       "d1",
		Date:         baseDay,
	})
	require.NoError(err)
	require.NotNil(doc)
	doc, err = GetDailyTaskDoc(DbTaskStatsId{
		Project:      "p1",
		Requester:    "r1",
		TaskName:     "task2",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseDay,
	})
	require.NoError(err)
	require.NotNil(doc)

	// Generate task stats for project p4 to check status aggregation
	err = GenerateDailyTaskStats(ctx, GenerateOptions{ProjectID: "p4", Requester: "r1", Window: baseHour, Tasks: []string{"task1"}, Runtime: jobTime})
	require.NoError(err)
	require.Equal(4, s.countDailyTaskDocs()) // 1 more task combination was added to the collection
	doc, err = GetDailyTaskDoc(DbTaskStatsId{
		Project:      "p4",
		Requester:    "r1",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseDay,
	})
	require.NoError(err)
	require.NotNil(doc)
	require.Equal(2, doc.NumSuccess)
	require.Equal(8, doc.NumFailed)
	require.Equal(1, doc.NumTestFailed)
	require.Equal(2, doc.NumSystemFailed)
	require.Equal(3, doc.NumSetupFailed)
	require.Equal(2, doc.NumTimeout)
	require.Equal(float64(150), doc.AvgDurationSuccess)
	require.WithinDuration(jobTime, doc.LastUpdate, 0)

	// Generate task for project p2 to check we get data for old tasks
	err = GenerateDailyTaskStats(ctx, GenerateOptions{ProjectID: "p2", Requester: "r1", Window: baseHour, Tasks: []string{"task1"}, Runtime: jobTime})
	require.NoError(err)
	require.Equal(5, s.countDailyTaskDocs()) // 1 more task combination was added to the collection
	doc, err = GetDailyTaskDoc(DbTaskStatsId{
		Project:      "p2",
		Requester:    "r1",
		TaskName:     "task1",
		BuildVariant: "v1",
		Distro:       "d1",
		Date:         baseDay,
	})
	require.NoError(err)
	require.NotNil(doc)
	require.Equal(1, doc.NumSuccess) // 1 old task
	require.Equal(3, doc.NumFailed)  // 2 tasks + 1 old tasks
	require.Equal(3, doc.NumTestFailed)
	require.Equal(0, doc.NumSystemFailed)
	require.Equal(0, doc.NumSetupFailed)
	require.Equal(0, doc.NumTimeout)
	require.Equal(float64(100), doc.AvgDurationSuccess)
	require.WithinDuration(jobTime, doc.LastUpdate, 0)
}

func (s *statsSuite) TestFindStatsToUpdate() {
	require := s.Require()

	// Insert task docs.
	s.initTasksToUpdate()

	// Find stats for p5 for a period with no finished tasks
	start := baseHour
	end := baseHour.Add(time.Hour)
	statsList, err := FindStatsToUpdate(FindStatsOptions{ProjectID: "p5", Requesters: nil, Start: start, End: end})
	require.NoError(err)
	require.Len(statsList, 0)

	// Find stats for p5 for a period around finish1
	start = finish1.Add(-1 * time.Hour)
	end = finish1.Add(time.Hour)
	statsList, err = FindStatsToUpdate(FindStatsOptions{ProjectID: "p5", Requesters: nil, Start: start, End: end})
	require.NoError(err)
	require.Len(statsList, 2)

	// Find stats for p5 for a period around finished1, filtering
	// by requester
	statsList, err = FindStatsToUpdate(FindStatsOptions{ProjectID: "p5", Requesters: []string{"r2"}, Start: start, End: end})
	require.NoError(err)
	require.Len(statsList, 1)
	statsList, err = FindStatsToUpdate(FindStatsOptions{ProjectID: "p5", Requesters: []string{"r1", "r2"}, Start: start, End: end})
	require.NoError(err)
	require.Len(statsList, 2)

	// The results are sorted so we know the order
	require.Equal("p5", statsList[0].ProjectId)
	require.Equal("r1", statsList[0].Requester)
	require.WithinDuration(util.GetUTCHour(commit1), statsList[0].Hour, 0)
	require.WithinDuration(util.GetUTCDay(commit1), statsList[0].Day, 0)
	require.Equal([]string{"task1"}, statsList[0].Tasks)

	// Find stats for p5 for a period around finish1
	start = finish1.Add(-1 * time.Hour)
	end = finish1.Add(time.Hour)
	statsList, err = FindStatsToUpdate(FindStatsOptions{ProjectID: "p5", Requesters: nil, Start: start, End: end})
	require.NoError(err)
	require.Len(statsList, 2)
	// The results are sorted so we know the order
	require.Equal("p5", statsList[0].ProjectId)
	require.Equal("r1", statsList[0].Requester)
	require.WithinDuration(util.GetUTCHour(commit1), statsList[0].Hour, 0)
	require.WithinDuration(util.GetUTCDay(commit1), statsList[0].Day, 0)
	require.Equal([]string{"task1"}, statsList[0].Tasks)
	require.Equal("p5", statsList[1].ProjectId)
	require.Equal("r2", statsList[1].Requester)
	require.WithinDuration(util.GetUTCHour(commit2), statsList[1].Hour, 0)
	require.WithinDuration(util.GetUTCDay(commit2), statsList[1].Day, 0)
	require.Len(statsList[1].Tasks, 3)
	require.Contains(statsList[1].Tasks, "task2")
	require.Contains(statsList[1].Tasks, "task2bis")
	require.Contains(statsList[1].Tasks, "task2old")
}

func (s *statsSuite) TestStatsToUpdate() {
	require := s.Require()

	stats1 := StatsToUpdate{"p1", "r1", baseHour, baseDay, []string{"task1", "task2"}}
	stats1bis := StatsToUpdate{"p1", "r1", baseHour, baseDay, []string{"task1", "task"}}
	stats1later := StatsToUpdate{"p1", "r1", baseHour.Add(time.Hour), baseDay, []string{"task1", "task2"}}
	stats1r2 := StatsToUpdate{"p1", "r2", baseHour, baseDay, []string{"task1", "task2"}}
	stats2 := StatsToUpdate{"p2", "r1", baseHour, baseDay, []string{"task1", "task2"}}

	// canMerge
	require.True(stats1.canMerge(&stats1))
	require.True(stats1.canMerge(&stats1bis))
	require.False(stats1.canMerge(&stats1later))
	require.False(stats1.canMerge(&stats1r2))
	// comparison
	require.True(stats1.lt(&stats2))
	require.True(stats1.lt(&stats1later))
	require.True(stats1.lt(&stats1r2))
	require.False(stats1.lt(&stats1))
	require.False(stats1.lt(&stats1bis))
	// merge
	merged := stats1.merge(&stats1bis)
	require.Equal(merged.ProjectId, stats1.ProjectId)
	require.Equal(merged.Requester, stats1.Requester)
	require.Equal(merged.Hour, stats1.Hour)
	require.Equal(merged.Day, stats1.Day)
	for _, t := range stats1.Tasks {
		require.Contains(merged.Tasks, t)
	}
	for _, t := range stats1bis.Tasks {
		require.Contains(merged.Tasks, t)
	}
}

/////////////////////////////////////////
// Methods to initialize database data //
/////////////////////////////////////////

func (s *statsSuite) initHourly() {
	hour1 := baseHour
	hour2 := baseHour.Add(time.Hour)
	hour3 := baseHour.Add(24 * time.Hour)
	s.insertHourlyTestStats("p1", "r1", "test1.js", "task1", "v1", "d1", hour1, 10, 5, 2, mgobson.NewObjectIdWithTime(hour1.Add(-time.Hour)))
	s.insertHourlyTestStats("p1", "r1", "test1.js", "task1", "v1", "d1", hour2, 20, 0, 5, mgobson.NewObjectIdWithTime(hour2.Add(-time.Hour)))
	s.insertHourlyTestStats("p1", "r1", "test1.js", "task1", "v1", "d1", hour3, 20, 0, 5, mgobson.NewObjectIdWithTime(hour3.Add(-time.Hour)))
}

func (s *statsSuite) insertHourlyTestStats(project string, requester string, testFile string, taskName string, variant string, distro string, date time.Time, numPass int, numFail int, avgDuration float64, lastID mgobson.ObjectId) {

	err := db.Insert(hourlyTestStatsCollection, bson.M{
		"_id": DbTestStatsId{
			Project:      project,
			Requester:    requester,
			TestFile:     testFile,
			TaskName:     taskName,
			BuildVariant: variant,
			Distro:       distro,
			Date:         date,
		},
		"num_pass":          numPass,
		"num_fail":          numFail,
		"avg_duration_pass": avgDuration,
		"last_id":           lastID,
	})
	s.Require().NoError(err)
}

type taskStatus struct {
	Status         string
	DetailsType    string
	DetailsTimeout bool
	TimeTaken      time.Duration
}

func (s *statsSuite) initTasks() {
	t0 := baseTime
	t0plus10m := baseTime.Add(10 * time.Minute)
	t0plus1h := baseTime.Add(time.Hour)
	t0min10m := baseTime.Add(-10 * time.Minute)
	t0min1h := baseTime.Add(-1 * time.Hour)
	success100 := taskStatus{"success", "test", false, 100 * 1000 * 1000 * 1000}
	success200 := taskStatus{"success", "test", false, 200 * 1000 * 1000 * 1000}
	testFailed := taskStatus{"failed", "test", false, 20}
	timeout := taskStatus{"failed", "test", true, 100}
	setupFailed := taskStatus{"failed", "setup", false, 10}
	systemFailed := taskStatus{"failed", "system", false, 10}

	// Task
        task := s.insertTask("p1", "r1", "task_id_1", 0, "task1", "v1", "d1", t0, t0.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_1", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
        s.insertTestResult("task_id_1", 0, "test2.js", evergreen.TestSilentlyFailedStatus, 120, &task, nil)
	s.insertTestResult("task_id_1", 0, "test3.js", evergreen.TestSucceededStatus, 10, &task, nil)
	// Task on variant v2
        task = s.insertTask("p1", "r1", "task_id_2", 0, "task1", "v2", "d1", t0, t0.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_2", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_2", 0, "test2.js", evergreen.TestFailedStatus, 120, &task, nil)
	// Task with different task name
        task = s.insertTask("p1", "r1", "task_id_3", 0, "task2", "v1", "d1", t0, t0.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_3", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_3", 0, "test2.js", evergreen.TestFailedStatus, 120, &task, nil)
	// Task 10 minutes later
        task = s.insertTask("p1", "r1", "task_id_4", 0, "task1", "v1", "d1", t0plus10m, t0plus10m.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_4", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_4", 0, "test2.js", evergreen.TestSucceededStatus, 120, &task, nil)
	s.insertTestResult("task_id_4", 0, "test3.js", evergreen.TestSucceededStatus, 15, &task, nil)
	// Task 1 hour later
        task = s.insertTask("p1", "r1", "task_id_5", 0, "task1", "v1", "d1", t0plus1h, t0plus1h.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_5", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
        // s.insertTestResult("task_id_5", 0, "test2.js", evergreen.TestFailedStatus, 120, &task, nil)
        s.insertTestResult("task_id_5", 0, "test2.js", evergreen.TestSucceededStatus, 120, &task, nil)
	// Task different requester
        task = s.insertTask("p1", "r2", "task_id_6", 0, "task1", "v1", "d1", t0, t0.Add(3*time.Second), testFailed)
	s.insertTestResult("task_id_6", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_6", 0, "test2.js", evergreen.TestFailedStatus, 120, &task, nil)
	// Task different project
        task = s.insertTask("p2", "r1", "task_id_7", 0, "task1", "v1", "d1", t0, t0.Add(3*time.Second), testFailed)
	s.insertTestResult("task_id_7", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_7", 0, "test2.js", evergreen.TestFailedStatus, 120, &task, nil)
	// Task with old executions.
        task = s.insertTask("p2", "r1", "task_id_8", 2, "task1", "v1", "d1", t0plus10m, t0plus10m.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_8", 2, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_8", 2, "test2.js", evergreen.TestFailedStatus, 120, &task, nil)
        task = s.insertOldTask("p2", "r1", "task_id_8", 0, "task1", "v1", "d1", t0min10m, t0min10m.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_8", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, nil)
	s.insertTestResult("task_id_8", 0, "test2.js", evergreen.TestSucceededStatus, 120, &task, nil)
	s.insertTestResult("task_id_8", 0, "testOld.js", evergreen.TestFailedStatus, 120, &task, nil)
        task = s.insertOldTask("p2", "r1", "task_id_8", 1, "task1", "v1", "d1", t0min1h, t0min10m.Add(3*time.Minute), success100)
	s.insertTestResult("task_id_8", 1, "test1.js", evergreen.TestSucceededStatus, 60, &task, nil)
	s.insertTestResult("task_id_8", 1, "test2.js", evergreen.TestSucceededStatus, 120, &task, nil)
	// Execution task
        task = s.insertTask("p3", "r1", "task_id_9", 0, "task_exec_1", "v1", "d1", t0, t0.Add(3*time.Minute), testFailed)
	// Display task
	displayTask := s.insertDisplayTask("p3", "r1", "task_id_10", 0, "task_display_1", "v1", "d1", t0, testFailed, []string{"task_id_9"})
	s.insertTestResult("task_id_9", 0, "test1.js", evergreen.TestFailedStatus, 60, &task, &displayTask)
	s.insertTestResult("task_id_9", 0, "test2.js", evergreen.TestSucceededStatus, 120, &task, &displayTask)
	// Project p4 used to test various task statuses
        s.insertTask("p4", "r1", "task_id_11", 0, "task1", "v1", "d1", t0, t0, success100)
        s.insertTask("p4", "r1", "task_id_12", 0, "task1", "v1", "d1", t0, t0, success200)
        s.insertTask("p4", "r1", "task_id_13", 0, "task1", "v1", "d1", t0, t0, testFailed)
        s.insertTask("p4", "r1", "task_id_14", 0, "task1", "v1", "d1", t0, t0, systemFailed)
        s.insertTask("p4", "r1", "task_id_15", 0, "task1", "v1", "d1", t0, t0, systemFailed)
        s.insertTask("p4", "r1", "task_id_16", 0, "task1", "v1", "d1", t0, t0, setupFailed)
        s.insertTask("p4", "r1", "task_id_17", 0, "task1", "v1", "d1", t0, t0, setupFailed)
        s.insertTask("p4", "r1", "task_id_18", 0, "task1", "v1", "d1", t0, t0, setupFailed)
        s.insertTask("p4", "r1", "task_id_19", 0, "task1", "v1", "d1", t0, t0, timeout)
        s.insertTask("p4", "r1", "task_id_20", 0, "task1", "v1", "d1", t0, t0, timeout)
	// Project p5 used to test handling of skipped tests.
        task = s.insertTask("p5", "r1", "task_id_5_1", 0, "task1", "v1", "d1", t0, t0.Add(3*time.Minute), success100)
	s.insertTestResult("task_id_5_1", 0, "test1.js", evergreen.TestSkippedStatus, 60, &task, nil)
	s.insertTestResult("task_id_5_1", 0, "test2.js", evergreen.TestSkippedStatus, 60, &task, nil)
        task = s.insertTask("p5", "r1", "task_id_5_2", 0, "task1", "v1", "d1", t0, t0.Add(3*time.Minute), testFailed)
	s.insertTestResult("task_id_5_2", 0, "test1.js", evergreen.TestSucceededStatus, 60, &task, nil)
	s.insertTestResult("task_id_5_2", 0, "test2.js", evergreen.TestFailedStatus, 60, &task, nil)
}

func (s *statsSuite) initTasksToUpdate() {
	s.insertFinishedTask("p5", "r1", "task1", commit1, finish1)
	s.insertFinishedTask("p5", "r2", "task2", commit2, finish1)
	s.insertFinishedTask("p5", "r2", "task2bis", commit2, finish1)
	s.insertFinishedOldTask("p5", "r2", "task2old", commit2, finish1)
	s.insertFinishedTask("p5", "r1", "task3", commit1, finish2)
	s.insertFinishedTask("p5", "r1", "task4", commit2, finish2)
}

func (s *statsSuite) insertTask(project string, requester string, taskId string, execution int, taskName string, variant string, distro string, createTime time.Time, finishTime time.Time, status taskStatus) task.Task {
	details := apimodels.TaskEndDetail{
		Status:   status.Status,
		Type:     status.DetailsType,
		TimedOut: status.DetailsTimeout,
	}
	newTask := task.Task{
		Id:           taskId,
		Execution:    execution,
		Project:      project,
		DisplayName:  taskName,
		Requester:    requester,
		BuildVariant: variant,
		DistroId:     distro,
		CreateTime:   createTime,
                FinishTime:   finishTime,
		Status:       status.Status,
		Details:      details,
		TimeTaken:    status.TimeTaken,
	}
	err := newTask.Insert()
	s.Require().NoError(err)
	return newTask
}

func (s *statsSuite) insertOldTask(project string, requester string, taskId string, execution int, taskName string, variant string, distro string, createTime time.Time, finishTime time.Time, status taskStatus) task.Task {
	details := apimodels.TaskEndDetail{
		Status:   status.Status,
		Type:     status.DetailsType,
		TimedOut: status.DetailsTimeout,
	}
	oldTaskId := taskId
	taskId = taskId + "_" + strconv.Itoa(execution)
	newTask := task.Task{
		Id:           taskId,
		Execution:    execution,
		Project:      project,
		DisplayName:  taskName,
		Requester:    requester,
		BuildVariant: variant,
		DistroId:     distro,
		CreateTime:   createTime,
                FinishTime:   finishTime,
		Status:       status.Status,
		Details:      details,
		TimeTaken:    status.TimeTaken,
		OldTaskId:    oldTaskId}
	err := db.Insert(task.OldCollection, &newTask)
	s.Require().NoError(err)
	return newTask
}

func (s *statsSuite) insertDisplayTask(project string, requester string, taskId string, execution int, taskName string, variant string, distro string, createTime time.Time, status taskStatus, executionTasks []string) task.Task {
	details := apimodels.TaskEndDetail{
		Status:   status.Status,
		Type:     status.DetailsType,
		TimedOut: status.DetailsTimeout,
	}
	newTask := task.Task{
		Id:             taskId,
		Execution:      execution,
		Project:        project,
		DisplayName:    taskName,
		Requester:      requester,
		BuildVariant:   variant,
		DistroId:       distro,
		CreateTime:     createTime,
		Status:         status.Status,
		Details:        details,
		TimeTaken:      status.TimeTaken,
		ExecutionTasks: executionTasks}
	err := newTask.Insert()
	s.Require().NoError(err)
	return newTask
}

func (s *statsSuite) insertTestResult(taskId string, execution int, testFile string, status string, durationSeconds int, theExecutionTask *task.Task, theDisplayTask *task.Task) {
	startTime := time.Now()
        if theExecutionTask != nil {
                startTime = theExecutionTask.CreateTime.Add(time.Second)
        }
	endTime := startTime.Add(time.Duration(durationSeconds) * time.Second)

	newTestResult := testresult.TestResult{
		TaskID:    taskId,
		Execution: execution,
		TestFile:  testFile,
		Status:    status,
		StartTime: float64(startTime.Unix()),
		EndTime:   float64(endTime.Unix()),
	}
	if theExecutionTask != nil {
		newTestResult.Project = theExecutionTask.Project
		newTestResult.BuildVariant = theExecutionTask.BuildVariant
		newTestResult.DistroId = theExecutionTask.DistroId
		newTestResult.Requester = theExecutionTask.Requester
		newTestResult.DisplayName = theExecutionTask.DisplayName
		newTestResult.TaskCreateTime = theExecutionTask.CreateTime
	}
	if theDisplayTask != nil {
		newTestResult.ExecutionDisplayName = theDisplayTask.DisplayName
	}
	err := newTestResult.Insert()
	s.Require().NoError(err)
}

func (s *statsSuite) insertFinishedTask(project string, requester string, taskName string, createTime time.Time, finishTime time.Time) {
	newTask := task.Task{
		Id:          mgobson.NewObjectId().Hex(),
		DisplayName: taskName,
		Project:     project,
		Requester:   requester,
		CreateTime:  createTime,
		FinishTime:  finishTime,
	}
	err := newTask.Insert()
	s.Require().NoError(err)
}

func (s *statsSuite) insertFinishedOldTask(project string, requester string, taskName string, createTime time.Time, finishTime time.Time) {
	newTask := task.Task{
		Id:          mgobson.NewObjectId().String(),
		DisplayName: taskName,
		Project:     project,
		Requester:   requester,
		CreateTime:  createTime,
		FinishTime:  finishTime,
	}
	err := db.Insert(task.OldCollection, &newTask)
	s.Require().NoError(err)
}

/////////////////////////////////////
// Methods to access database data //
/////////////////////////////////////

func (s *statsSuite) countMatchingDocs(collection string) int {
        count, err := db.Count(collection, bson.M{})
        s.Require().NoError(err)
        return count
}

func (s *statsSuite) countDocs(collection string) int {
	count, err := db.Count(collection, bson.M{})
	s.Require().NoError(err)
	return count
}

func (s *statsSuite) countDailyTestDocs() int {
	return s.countDocs(dailyTestStatsCollection)
}

func (s *statsSuite) countHourlyTestDocs() int {
	return s.countDocs(hourlyTestStatsCollection)
}

func (s *statsSuite) countDailyTaskDocs() int {
	return s.countDocs(DailyTaskStatsCollection)
}

func (s *statsSuite) getLastTestResult(testStatsID DbTestStatsId, hourly bool) (*testresult.TestResult, error) {
	var lastTestResult *testresult.TestResult
	start := util.GetUTCHour(testStatsID.Date)
	end := start.Add(time.Hour)
        if !hourly {
                start = util.GetUTCDay(testStatsID.Date)
                end = start.Add(24 * time.Hour)
        }

        qry := bson.M{
                testresult.ProjectKey:   testStatsID.Project,
                testresult.RequesterKey: testStatsID.Requester,
		testresult.TestFileKey:  testStatsID.TestFile,
		"$or": []bson.M{
			{testresult.DisplayNameKey: testStatsID.TaskName},
			{testresult.ExecutionDisplayNameKey: testStatsID.TaskName},
		},
		testresult.DistroIdKey:     testStatsID.Distro,
		testresult.BuildVariantKey: testStatsID.BuildVariant,
		testresult.TaskCreateTimeKey: bson.M{
			"$gte": start,
			"$lt":  end,
		},
	}
	q := db.Query(qry).Sort([]string{"-_id"}).Limit(1)
	testResults, err := testresult.Find(q)
	if err != nil || len(testResults) == 0 {
		lastTestResult = nil
	} else {
		lastTestResult = &testResults[0]
	}
	return lastTestResult, err
}

func (s *statsSuite) getLastHourlyTestStat(testStatsID DbTestStatsId) (*DbTestStats, error) {
        var lastTestStats *DbTestStats
        testResults := []DbTestStats{}

	start := util.GetUTCDay(testStatsID.Date)
	end := start.Add(24 * time.Hour)

	qry := bson.M{
		"_id." + dbTestStatsIdProjectKey:      testStatsID.Project,
		"_id." + dbTestStatsIdRequesterKey:    testStatsID.Requester,
		"_id." + dbTestStatsIdTestFileKey:     testStatsID.TestFile,
		"_id." + DbTestStatsIdTaskNameKey:     testStatsID.TaskName,
		"_id." + DbTestStatsIdDistroKey:       testStatsID.Distro,
		"_id." + DbTestStatsIdBuildVariantKey: testStatsID.BuildVariant,
		"_id." + dbTestStatsIdDateKey: bson.M{
			"$gte": start,
			"$lt":  end,
		},
	}
	err := db.FindAll(hourlyTestStatsCollection, qry, db.NoProjection, []string{"-last_id"}, db.NoSkip, 1, &testResults)
	if adb.ResultsNotFound(err) {
		return nil, nil
	}

        if err != nil || len(testResults) == 0 {
		lastTestStats = nil
	} else {
		lastTestStats = &testResults[0]
	}
	return lastTestStats, err
}

func (s *statsSuite) validateDbTestStats(testStatsID DbTestStatsId, date time.Time, numPass int, numFail int, avgDurationPass float64, lastUpdate time.Time, hourly bool) {
        require := s.Require()

        doc := s.validateDbTestStatsNoLast(testStatsID, date, numPass, numFail, avgDurationPass, lastUpdate, hourly)
        lastTestResult, err := s.getLastTestResult(testStatsID, hourly)
        require.NoError(err)
        require.Equal(lastTestResult.ID, doc.LastID)
}

func (s *statsSuite) validateDbTestStatsNoLast(testStatsID DbTestStatsId, date time.Time, numPass int, numFail int, avgDurationPass float64, lastUpdate time.Time, hourly bool) *DbTestStats {
        require := s.Require()
        var doc *DbTestStats
        var err error

        if hourly {
                doc, err = GetHourlyTestDoc(testStatsID)
        } else {
                doc, err = GetDailyTestDoc(testStatsID)
        }
        require.Nil(err)
        require.NotNil(doc)
        require.Equal(testStatsID.Project, doc.Id.Project)
        require.Equal(testStatsID.Requester, doc.Id.Requester)
        require.Equal(testStatsID.TestFile, doc.Id.TestFile)
        require.Equal(testStatsID.TaskName, doc.Id.TaskName)
        require.Equal(testStatsID.BuildVariant, doc.Id.BuildVariant)
        require.Equal(testStatsID.Distro, doc.Id.Distro)
        require.Equal(date, doc.Id.Date.UTC())
        require.Equal(numPass, doc.NumPass)
        require.Equal(numFail, doc.NumFail)
        require.Equal(avgDurationPass, doc.AvgDurationPass)
        require.WithinDuration(lastUpdate, doc.LastUpdate, 0)

        return doc
}
