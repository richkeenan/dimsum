package control

import "github.com/richkeenan/dimsum/internal/clients"

type IconList struct {
	Items []string `json:"items"`
}

func (s *Service) Icons(search string) IconList {
	return IconList{Items: clients.IconNames(search)}
}
