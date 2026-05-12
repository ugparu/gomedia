# logger

Shared logrus-based implementation of `utils/logger.Logger` used by the other examples. Not a runnable program — it's imported by sibling example packages.

The formatter writes lines as `|<component>|<message>`, where the component is taken from the first argument to each log call (typically the calling struct's `String()` value). Copy or adapt for your own application.
