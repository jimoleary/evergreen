mciModule.factory('PerfDiscoveryDataService', function(
  $q, $window, ApiV1, ApiV2, ApiBuildBaron, ApiTaskdata, EVG,
  MPA_UI, PERF_DISCOVERY
) {
  var PD = PERF_DISCOVERY

  /*********************
   * UTILITY FUNCTIONS *
   *********************/

  var respData = function(resp) {
    return resp.data
  }

  function slug(data) {
    return [
      data.build,
      data.storageEngine,
      data.task, data.test,
      data.threads
    ].join('-')
  }

  // TODO remove the line and related code once `storageEngine` will be added
  // to the API (see EVG-2744)
  var seRe = new RegExp('[_ -](wt|wiredtiger|mmapv1|inmemory)$', 'i')

  // Takes build and task names
  // Extrcts storage engine from build/tasks name
  // :returns: dict of {storageEngine, task, build}
  // returned task and build names could be different
  function extractStorageEngine(build, task) {
    var storageEngine,
        attrs = {
          build: build,
          task: task,
        }

    if (build == undefined) return {}

    _.any(attrs, function(v, k) {
      var match = v.match(seRe)
      if (match) {
        storageEngine = match[1]
        attrs[k] = attrs[k].slice(0, match.index)
        return true
      }
    })

    // Set default
    if(!storageEngine) storageEngine = '(none)'

    return _.extend({storageEngine: storageEngine}, attrs)
  }

  // Calculates ratio for given test result `speed` and `baseSpeed` reference
  function ratio(speed, baseSpeed) {
    var ratio = speed / baseSpeed
    if (speed >= 0) {
      return ratio
    } else {
      // Negatives mean latency in ms. Invert the ratio so higher is better.
      return 1 / ratio
    }
  }

  function noBaselineFilter(results) {
    return function(item, id) {
      return results.baseline[id] == undefined
    }
  }

  function isShortItemId(id) {
    return id.length == EVG.PATCH_ID_LEN
  }

  function isLongVersionId(id) {
    return id.indexOf('_') > 0 && id.length > EVG.PATCH_ID_LEN
  }

  function isGitHash(id) {
    return id.length == EVG.GIT_HASH_LEN
  }

  function githashToVersionId(githash) {
    return $window.project + '_' + githash
  }

  // For given `query` attempts to construct valid selctable
  // comparison item. It could be version short/long id, githash
  // Should not be used for tags (all tags are available on ui)
  function getQueryBasedItem(query) {
    var GITHASH = 'h'
    var ID_SHORT = 's'
    var ID_LONG = 'l'

    if (query == undefined) return

    var idKind = (
      isGitHash(query) ? GITHASH :
      isShortItemId(query) ? ID_SHORT :
      isLongVersionId(query) ? ID_LONG : undefined
    )

    var uniformId = (
      idKind == GITHASH ? githashToVersionId(query) :
      _.contains([ID_SHORT, ID_LONG], idKind) ? query : undefined
    )

    // There are not enough information to differentiate
    // version id from patch id
    var kind = PD.KIND_VERSION

    if (uniformId) {
      return {
        id: uniformId,
        name: query,
        kind: kind,
      }
    } else {
      return undefined
    }
  }

  /******************
   * DATA PROCESING *
   ******************/

  // :param item: test result data item
  // :param receiver: dict which will receive processed results
  // :param ctx: dict of {build, task, storageEngine} see `extractTasks`
  // returns: could be ignored
  function processItem(item, receiver, ctx) {
    var parts = item.name.split('-')
    // At some point we renamed the tests to remove -wiredTiger and -MMAPv1 suffixs.
    // Normalize old names to match the new ones
    if (parts.length == 2 && _.any('wWmM', _.bind(''.startsWith, parts[0]))) {
      item.name = parts[0]
    }

    return _.chain(item.results)
      .omit(_.isString)
      .each(function(speed, threads) {
        var extracted = extractStorageEngine(ctx.buildName, ctx.taskName)
        var data = {
          build: extracted.build,
          task: extracted.task,
          taskURL: MPA_UI.TASK_BY_ID({task_id: ctx.taskId}),
          buildURL: MPA_UI.BUILD_BY_ID({build_id: ctx.buildId}),
          taskId: ctx.taskId,
          test: item.name,
          threads: +threads,
          speed: speed.ops_per_sec,
          storageEngine: extracted.storageEngine,
        }
        receiver[slug(data)] =  data
      })
  }

  // data is [
  //   {
  //     current: <item>,
  //     baseline: <item>,
  //     history: [<item>, ...],
  //     ctx: <context obj>
  //   },
  //   {...}
  // ]
  function onProcessData(data) {
    var now = {}, baseline = {}, history = {}

    _.each(data, function(d) {
      // Skip empty items
      if (d == null) return

      // TODO add group validation instead of individual
      // Process current (revision) data
      if (d.current) {
        _.each(d.current.data.results, function(result) {
          processItem(result, now, d.ctx)
        })
      }

      // Process baseline data
      if (d.baseline) {
        _.each(d.baseline.data.results, function(result) {
          processItem(result, baseline, d.ctx)
        })
      }

      // Process history data
      _.each(d.history, function(histItems) {
        // Create an empty 'bucket' for history items if not exists
        var order = histItems.order
        if (history[order] == undefined) {
          history[order] = {}
        }
        _.each(histItems.data.results, function(result) {
          processItem(result, history[order], d.ctx)
        })
      })
    })

    return {
      now: now,
      baseline: baseline,
      // Sorts history items by `order` and returns the list
      history: _.map(
        _.sortBy(_.keys(history)),
        function(key) { return history[key] }
      ),
    }
  }

  function onGetRows(results) {
    return _.chain(results.now)
      .omit(noBaselineFilter(results))
      .map(function(item, id) {
        var baseline = results.baseline[id]
        var appendix = {
          ratio: ratio(item.speed, baseline.speed),
          baseSpeed: baseline.speed,
        }

        var trendData = _.chain(results.history)
          .take(100)
          .map(function(run) {
            var base = run[id]
            if (base == undefined || !base.speed) {
              return 1
            }
            return ratio(item.speed, base.speed)
          })
          .value()

        // Trend chart data
        appendix.trendData = trendData.concat(appendix.ratio)

        var avgRatio = d3.mean(trendData)
        appendix.avgRatio = avgRatio
        appendix.avgVsSelf = [avgRatio, appendix.ratio]
        return _.extend({}, item, appendix)
      })
      .value()
  }

  // TODO Should be part of `VersionResource`
  // :parm version: version object
  // :returns: Array of {taskId, taskName, buildName}
  function extractTasks(version) {
    return _.reduce(version.builds, function(items, build) {
      // Going over dict of tasks ({taskName: data})
      return items.concat(_.map(build.tasks, function(taskData, taskName) {
        return {
          taskId: taskData.task_id,
          buildId: build.id,
          taskName: taskName,
          buildName: build.name,
        }
      }))
    }, [])
  }

  function queryBuildData(version) {
    return $q.all(
      _.map(version.build_variants_status, function(d) {
        return ApiV1.getBuildDetail(d.build_id).then(
          respData, function(e) { return {} }
        )
      })
    )
  }

  function tasksOfBuilds(promise) {
    return promise.then(function(builds) {
      return extractTasks({builds: builds})
    })
  }

  function versionSelectAdaptor(versionsRes) {
    return _.chain(versionsRes.data.versions)
      .where({rolled_up: false})
      .map(function(d) {
        return {
          kind: PD.KIND_VERSION,
          id: d.versions[0].version_id,
          name: d.versions[0].revision,
        }
      })
      .value()
  }

  function tagSelectAdaptor(tagsRes) {
    return _.map(tagsRes.data, function(d) {
      return {kind: PD.KIND_TAG, id: d.obj.version_id, name: d.name}
    })
  }

  /******************
   * PROMISE CHAINS *
   ******************/

  // :param rows: list of grid rows
  // :rtype: $q.promise({taskId: {key, link}, ...})
  function getBFTicketsForRows(rows) {
    var uniqueTaskIds = _.chain(rows)
      .uniq(false, function(d) {
        return d.taskId
      })
      .pluck('taskId')
      .value()

    // Execute all as {task_id: promise, ...}
    return $q.all(
      _.reduce(uniqueTaskIds, function(m, d) {
        m[d] = ApiBuildBaron.getTicketsByTaskId(d)
        return m
      }, {})
    )
    .then(function(res) { // Then refine the results
      return _.mapObject(res, function(d) {
        if (d.data) {
          return _.map(_.pluck(d.data, 'key'), function(key) {
            return {key: key}
          })
        } else {
          return []
        }
      })
    })
  }

  // Makes series of HTTP calls and loads curent, baseline and history data
  // :rtype: $q.promise
  function queryData(tasksPromise, baselineTasks) {
    return $q.all({
      tasks: tasksPromise,
      baselineTasks: baselineTasks
    }).then(function(promise) {
      return $q.all(_.map(promise.tasks, function(task) {
        return ApiTaskdata.getTaskById(task.taskId, 'perf')
          .then(respData, function() { return null })
          .then(function(data) {
            if (data) {
              var baseline = _.findWhere(promise.baselineTasks, {
                buildName: task.buildName,
                taskName: task.taskName,
              })
              // the name may have been changed to remove '_WT'
              if (!baseline)
                baseline = _.findWhere(promise.baselineTasks, {
                  buildName: task.buildName,
                  taskName: task.taskName + '_WT',
                })
              // Skip items with no baseline results
              if (!baseline) return null

              return $q.all({
                ctx: $q.resolve(task),
                current: data,
                history: ApiTaskdata.getTaskHistory(task.taskId, 'perf')
                  .then(respData, function(e) { return [] }),
                baseline: ApiTaskdata.getTaskById(baseline.taskId, 'perf')
                  .then(respData, function(e) { return null }),
              })
            } else {
              return null
            }
          })
      }))
    })
  }

  // Processes `queryData` return value
  // Populates dicts of current (now, baseline) and history result items
  // returns dict of {now, baseline, history} test result items
  function processData(promise) {
    return promise.then(onProcessData)
  }

  // Returns data for the (ui) grid
  // :param promise: returned by `processData`
  function getRows(promise) {
    return promise.then(onGetRows)
  }

  /**************
   * PUBLIC API *
   **************/

  // Queries list of available options for compare from/to dropdowns
  // :returns: (Promise of) list of versions/tags
  // :rtype: Promise([{
  //    kind: 't(ag)|v(ersion)',
  //    id: 'version_id',
  //    name: 'display name'
  // }, ...])
  function getComparisionOptions(projectId) {
    return $q.all([
      ApiV2.getRecentVersions(projectId).then(versionSelectAdaptor),
      ApiTaskdata.getProjectTags(projectId).then(tagSelectAdaptor)
    ]).then(function(data) {
      return Array.prototype.concat.apply([], data)
    })
  }

  function findVersionItem(items, query) {
    if (query == undefined) return

    return _.find(items, function(d) {
      // Checks if query is inside or equal an id
      // Useful for long verion ids and revisions
      // e.g. $GITHASH will match sys-perf_$GITHASH
      return d.id.indexOf(query) > -1 || query == d.name
    })
  }

  // This function is used to make possible to type arbitrary
  // version revision into version drop down
  function getVersionOptions(items, query) {
    var found = findVersionItem(items, query)
    if (!found) {
      var queryBased = getQueryBasedItem(query)
      if (queryBased) {
        return items.concat(queryBased)
      }
    }
    return items
  }

  function getCompItemVersion(compItem) {
    return ApiV2.getVersionById(compItem.id)
      // Extract version object
      .then(function(res) {
        return res.data
      })
  }

  function getData(version, baselineTag) {
    return getRows(
      processData(
        queryData(
          tasksOfBuilds(queryBuildData(version)),
          tasksOfBuilds(queryBuildData(baselineTag))
        )
      )
    )
  }

  return {
    // public api
    getData: getData,
    getComparisionOptions: getComparisionOptions,
    findVersionItem: findVersionItem,
    getVersionOptions: getVersionOptions,
    getCompItemVersion: getCompItemVersion,
    getQueryBasedItem: getQueryBasedItem,
    getBFTicketsForRows: getBFTicketsForRows,

    // For teting
    _extractTasks: extractTasks,
    _extractStorageEngine: extractStorageEngine,
    _processItem: processItem,
    _onProcessData: onProcessData,
    _onGetRows: onGetRows,
    _versionSelectAdaptor: versionSelectAdaptor,
    _tagSelectAdaptor: tagSelectAdaptor,
  }
})
