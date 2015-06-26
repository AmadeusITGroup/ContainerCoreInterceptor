2.0: Released by `jsilvestro` on `2016-02-15`.
----------------------------------------------------------------------------
# New features:

- Updated all dependecies
- Move to go 1.6
- Improved logging and error handling


#Bug fixes:

- Fix empty notification issue introduced  with 1.4

1.4: Released by `jsilvestro` on `2016-02-15`.
----------------------------------------------------------------------------
# New features:

- Remove all column characters from image name (column is the tag separator)

1.3: Released by `jsilvestro` on `2016-02-15`.
----------------------------------------------------------------------------
# New features:

- adjust fatal error message

1.2: Released by `jsilvestro` on `2016-02-07`.
----------------------------------------------------------------------------
# Bug fixes:

- Logs and error if it cannot listen of Docker socket and retries with an increasing timeout.

1.1: Released by `jsilvestro` on `2016-01-29`.
----------------------------------------------------------------------------
# New features:

- Logging of critical error also to stdout (if enabled in glog)

1.0: First release
------------------
- Intercepts both corde dump and java heap dump
- Can notify kafka or a REST api
- Can store the cored container to a docker registry
