package deps

import (
	_ "github.com/pkg/sftp"
	_ "github.com/sirupsen/logrus"
	_ "github.com/spf13/afero"
	_ "github.com/spf13/cobra"
	_ "github.com/spf13/viper"
	_ "github.com/stretchr/testify/assert"
	_ "github.com/vbauerster/mpb/v8"
	_ "golang.org/x/crypto/ssh"
	_ "golang.org/x/time/rate"
)
