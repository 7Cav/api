package cmd

import (
	"fmt"
	"github.com/7cav/api/servers"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Launches the api servers",
	Run: func(cmd *cobra.Command, args []string) {
		server := servers.New(fmt.Sprintf("0.0.0.0:%s", viper.GetString("port")))
		server.Start()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
